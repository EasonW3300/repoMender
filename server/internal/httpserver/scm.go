package httpserver

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/automations"
	"github.com/EasonW3300/repoMender/server/internal/diagnosis"
	"github.com/EasonW3300/repoMender/server/internal/repair"
	"github.com/EasonW3300/repoMender/server/internal/review"
	"github.com/EasonW3300/repoMender/server/internal/scm"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// auth sessions and CSRF checks protect connection mutations. scm.Service
// performs provider-neutral authorization, synchronization, and webhook logic.

func (s *Server) scmProviders(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireSession(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.scm.Providers()})
}

func (s *Server) scmConnections(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	connections, err := s.scm.ListConnections(r.Context(), session.User)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scm_connections_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": connections})
}

func (s *Server) scmConnect(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	location, err := s.scm.BeginConnect(r.Context(), session.User,
		scm.Provider(r.PathValue("provider")), r.URL.Query().Get("returnTo"))
	if errors.Is(err, scm.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if errors.Is(err, scm.ErrProviderUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "scm_provider_unavailable")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scm_connect_failed")
		return
	}
	http.Redirect(w, r, location, http.StatusFound)
}

func (s *Server) scmCallback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("error") != "" {
		writeError(w, http.StatusBadRequest, "scm_authorization_rejected")
		return
	}
	provider := scm.Provider(r.PathValue("provider"))
	connection, returnTo, err := s.scm.CompleteConnect(r.Context(), provider,
		r.URL.Query().Get("state"), scm.Callback{
			Code: r.URL.Query().Get("code"), InstallationID: r.URL.Query().Get("installation_id"),
		})
	if errors.Is(err, scm.ErrInvalidFlow) {
		writeError(w, http.StatusBadRequest, "scm_flow_invalid")
		return
	}
	if errors.Is(err, scm.ErrProviderUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "scm_provider_unavailable")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "scm_callback_failed")
		return
	}
	target, _ := url.Parse(returnTo)
	query := target.Query()
	query.Set("connected", string(connection.Provider))
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Server) scmSync(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !auth.VerifyCSRF(session, r.Header.Get("X-CSRF-Token")) {
		writeError(w, http.StatusForbidden, "csrf_failed")
		return
	}
	connection, err := s.scm.Sync(r.Context(), session.User, r.PathValue("id"))
	if errors.Is(err, scm.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if errors.Is(err, scm.ErrProviderUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "scm_provider_unavailable")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "scm_sync_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connection": connection})
}

func (s *Server) repositories(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	repositories, err := s.scm.ListRepositories(r.Context(), session.User)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "repositories_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": repositories})
}

func (s *Server) repository(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	repository, err := s.scm.GetRepository(r.Context(), session.User, r.PathValue("id"))
	if errors.Is(err, scm.ErrRepositoryNotFound) {
		writeError(w, http.StatusNotFound, "repository_not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "repository_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repository": repository})
}

func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	s.handleWebhook(w, r, scm.ProviderGitHub)
}

func (s *Server) gitlabWebhook(w http.ResponseWriter, r *http.Request) {
	s.handleWebhook(w, r, scm.ProviderGitLab)
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request, provider scm.Provider) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "webhook_too_large")
		return
	}
	if (s.review != nil || s.diagnosis != nil || s.repair != nil || s.automations != nil) && provider == scm.ProviderGitHub {
		event, err := s.scm.VerifyWebhook(provider, r.Header, body)
		if errors.Is(err, scm.ErrInvalidWebhook) {
			writeError(w, http.StatusUnauthorized, "webhook_invalid")
			return
		}
		if errors.Is(err, scm.ErrProviderUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "scm_provider_unavailable")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "webhook_failed")
			return
		}
		if s.automations != nil {
			matched, matchErr := s.automations.Matching(r.Context(), string(provider), event.EventType,
				normalizedString(event, "action"), webhookBranch(event), normalizedString(event, "repository"))
			if matchErr != nil {
				writeError(w, http.StatusInternalServerError, "automation_lookup_failed")
				return
			}
			if len(matched) > 0 {
				actor, actorErr := s.auth.SystemActor(r.Context())
				if actorErr != nil {
					writeError(w, http.StatusForbidden, "automation_actor_unavailable")
					return
				}
				taskPayload, payloadErr := webhookTaskPayload(event)
				if payloadErr != nil {
					writeError(w, http.StatusBadRequest, "automation_event_invalid")
					return
				}
				triggerKey := event.DeliveryID
				if triggerKey == "" {
					digest := sha256.Sum256(body)
					triggerKey = fmt.Sprintf("body:%x", digest[:])
				}
				createdTasks := make([]tasks.Task, 0, len(matched))
				for _, template := range matched {
					result, triggerErr := s.automations.Trigger(r.Context(), automations.TriggerInput{
						TemplateID: template.ID, TriggerKey: triggerKey, Provider: string(provider), Event: event.EventType,
						Action: normalizedString(event, "action"), Branch: webhookBranch(event),
						Repository: normalizedString(event, "repository"), RepositoryID: normalizedString(event, "repositoryId"),
						Title: webhookTaskTitle(event, template.Name), TaskPayload: taskPayload, SourcePayload: body,
						ActorID: actor.ID,
					})
					if triggerErr != nil && !errors.Is(triggerErr, automations.ErrDuplicateTrigger) {
						writeAutomationError(w, triggerErr)
						return
					}
					if template.Kind == tasks.KindIssueRepair && s.repair != nil && !result.Duplicate {
						if _, bindErr := s.repair.BindAutomationTask(r.Context(), result.Task, webhookIssue(event), actor.ID); bindErr != nil {
							writeError(w, http.StatusBadRequest, "automation_issue_repair_invalid")
							return
						}
					}
					createdTasks = append(createdTasks, result.Task)
				}
				created, recordErr := s.scm.RecordWebhook(r.Context(), provider, event, body)
				if recordErr != nil {
					writeError(w, http.StatusInternalServerError, "webhook_failed")
					return
				}
				if !created {
					writeJSON(w, http.StatusAccepted, map[string]string{"status": "duplicate", "provider": "github"})
					return
				}
				writeJSON(w, http.StatusAccepted, map[string]any{"status": "automation_tasks_created", "provider": "github", "tasks": createdTasks})
				return
			}
			// Once M9 is enabled, templates are the sole scheduling authority;
			// falling through to legacy direct triggers would bypass a disabled or
			// out-of-scope automation configuration.
			created, recordErr := s.scm.RecordWebhook(r.Context(), provider, event, body)
			if recordErr != nil {
				writeError(w, http.StatusInternalServerError, "webhook_failed")
				return
			}
			if !created {
				writeJSON(w, http.StatusAccepted, map[string]string{"status": "duplicate", "provider": "github"})
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "provider": "github"})
			return
		}
		var task tasks.Task
		var actionable bool
		if s.review != nil {
			task, actionable, err = s.review.HandleWebhook(r.Context(), event)
			if errors.Is(err, review.ErrUnsupportedEvent) {
				actionable = false
				err = nil
			}
		}
		if !actionable && s.diagnosis != nil {
			var diagnosisTask tasks.Task
			diagnosisTask, actionable, err = s.diagnosis.HandleWebhook(r.Context(), event)
			if errors.Is(err, diagnosis.ErrUnsupportedEvent) {
				actionable = false
				err = nil
			}
			if actionable {
				task = diagnosisTask
			}
		}
		if !actionable && s.repair != nil && event.EventType == "issues" {
			action := normalizedString(event, "action")
			if action == "opened" || action == "reopened" {
				repository, lookupErr := s.scm.FindRepository(r.Context(), scm.ProviderGitHub, normalizedString(event, "repositoryId"))
				if lookupErr != nil {
					err = lookupErr
				} else {
					actor, actorErr := s.auth.SystemActor(r.Context())
					if actorErr != nil {
						err = actorErr
					} else {
						number, parseErr := strconv.Atoi(normalizedString(event, "issueNumber"))
						if parseErr != nil {
							err = parseErr
						} else {
							createdRepair, createErr := s.repair.CreateFromIssue(r.Context(), repair.Issue{
								Provider: "github", RepositoryID: repository.ID, Repository: normalizedString(event, "repository"),
								CloneURL: normalizedString(event, "cloneURL"), WebURL: normalizedString(event, "webURL"),
								InstallationID: normalizedString(event, "installationId"), Number: number,
								Title: normalizedString(event, "issueTitle"), Body: normalizedString(event, "issueBody"),
								State: normalizedString(event, "issueState"), IsPullRequest: normalizedString(event, "isPullRequest") == "true",
								BaseBranch: normalizedString(event, "defaultBranch")}, actor.ID)
							if createErr != nil {
								err = createErr
							} else {
								task, err = s.tasks.Get(r.Context(), createdRepair.TaskID)
								actionable = err == nil
							}
						}
					}
				}
			}
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "review_event_invalid")
			return
		}
		if !actionable {
			created, recordErr := s.scm.RecordWebhook(r.Context(), provider, event, body)
			if recordErr != nil {
				writeError(w, http.StatusInternalServerError, "webhook_failed")
				return
			}
			if !created {
				writeJSON(w, http.StatusAccepted, map[string]string{"status": "duplicate", "provider": "github"})
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "provider": "github"})
			return
		}
		created, err := s.scm.RecordWebhook(r.Context(), provider, event, body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "webhook_failed")
			return
		}
		if !created {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "duplicate", "provider": "github"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "task_created", "provider": "github", "task": task})
		return
	}
	created, err := s.scm.HandleWebhook(r.Context(), provider, r.Header, body)
	if errors.Is(err, scm.ErrInvalidWebhook) {
		writeError(w, http.StatusUnauthorized, "webhook_invalid")
		return
	}
	if errors.Is(err, scm.ErrProviderUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "scm_provider_unavailable")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "webhook_failed")
		return
	}
	status := "accepted"
	if !created {
		status = "duplicate"
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status": status, "provider": strings.ToLower(string(provider)),
	})
}

func normalizedString(event scm.WebhookEvent, key string) string {
	if value, ok := event.Normalized[key]; ok {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	return ""
}

func webhookBranch(event scm.WebhookEvent) string {
	if branch := normalizedString(event, "baseBranch"); branch != "" {
		return branch
	}
	return normalizedString(event, "defaultBranch")
}

func webhookTaskTitle(event scm.WebhookEvent, templateName string) string {
	repository := normalizedString(event, "repository")
	switch event.EventType {
	case "pull_request":
		return fmt.Sprintf("%s · %s#%s", templateName, repository, normalizedString(event, "pullRequestNumber"))
	case "workflow_run":
		return fmt.Sprintf("%s · %s run %s", templateName, repository, normalizedString(event, "workflowRunId"))
	default:
		return fmt.Sprintf("%s · %s issue %s", templateName, repository, normalizedString(event, "issueNumber"))
	}
}

func webhookTaskPayload(event scm.WebhookEvent) (json.RawMessage, error) {
	payload := map[string]any{
		"provider": "github", "repositoryId": normalizedString(event, "repositoryId"),
		"repositoryName": normalizedString(event, "repository"), "cloneURL": normalizedString(event, "cloneURL"),
		"webURL": normalizedString(event, "webURL"), "installationId": normalizedString(event, "installationId"),
		"action": normalizedString(event, "action"), "headSHA": normalizedString(event, "headSHA"),
		"headBranch": normalizedString(event, "headBranch"), "baseBranch": normalizedString(event, "baseBranch"),
		"author": normalizedString(event, "author"), "workflowName": normalizedString(event, "workflowName"),
		"conclusion": normalizedString(event, "conclusion"), "issueTitle": normalizedString(event, "issueTitle"),
		"issueBody": normalizedString(event, "issueBody"), "issueState": normalizedString(event, "issueState"),
		"defaultBranch": normalizedString(event, "defaultBranch"), "source": "automation",
	}
	if event.EventType == "pull_request" {
		value, err := strconv.Atoi(normalizedString(event, "pullRequestNumber"))
		if err != nil || value <= 0 {
			return nil, err
		}
		payload["pullRequestNumber"] = value
	}
	if event.EventType == "workflow_run" {
		value, err := strconv.ParseInt(normalizedString(event, "workflowRunId"), 10, 64)
		if err != nil || value <= 0 {
			return nil, err
		}
		payload["workflowRunId"] = value
	}
	if event.EventType == "issues" {
		value, err := strconv.Atoi(normalizedString(event, "issueNumber"))
		if err != nil || value <= 0 {
			return nil, err
		}
		payload["issueNumber"] = value
	}
	encoded, err := json.Marshal(payload)
	return encoded, err
}

func webhookIssue(event scm.WebhookEvent) repair.Issue {
	return repair.Issue{
		Provider: "github", RepositoryID: normalizedString(event, "repositoryId"), Repository: normalizedString(event, "repository"),
		CloneURL: normalizedString(event, "cloneURL"), WebURL: normalizedString(event, "webURL"),
		InstallationID: normalizedString(event, "installationId"), Number: mustAtoi(normalizedString(event, "issueNumber")),
		Title: normalizedString(event, "issueTitle"), Body: normalizedString(event, "issueBody"),
		State: normalizedString(event, "issueState"), IsPullRequest: normalizedString(event, "isPullRequest") == "true",
		BaseBranch: normalizedString(event, "defaultBranch"), BaseSHA: normalizedString(event, "headSHA"),
	}
}

func mustAtoi(value string) int {
	parsed, _ := strconv.Atoi(value)
	return parsed
}
