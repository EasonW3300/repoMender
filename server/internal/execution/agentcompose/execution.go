package agentcompose

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/execution"
)

// bytes, encoding/binary, and encoding/json implement Connect's documented
// JSON streaming envelopes. The provider-neutral execution package keeps these
// wire details out of RepoMender task orchestration.

const (
	startRunProcedure      = "/agentcompose.v2.RunService/StartRun"
	getRunProcedure        = "/agentcompose.v2.RunService/GetRun"
	followRunLogsProcedure = "/agentcompose.v2.RunService/FollowRunLogs"
	stopRunProcedure       = "/agentcompose.v2.RunService/StopRun"

	connectUnaryJSON     = "application/json"
	connectStreamingJSON = "application/connect+json"
	connectEndStreamFlag = byte(0x02)
	maxConnectFrameBytes = 4 << 20
)

const diagnosticOutputSchema = `{"type":"object","required":["schemaVersion","summary"],"properties":{"schemaVersion":{"type":"string","const":"v1"},"summary":{"type":"string"}},"additionalProperties":false}`

type runAgentRequest struct {
	ProjectID        string `json:"projectId"`
	AgentName        string `json:"agentName"`
	Prompt           string `json:"prompt"`
	Source           string `json:"source"`
	CleanupPolicy    string `json:"cleanupPolicy"`
	OutputSchemaJSON string `json:"outputSchemaJson"`
	ClientRequestID  string `json:"clientRequestId"`
	Driver           string `json:"driver,omitempty"`
	PayloadJSON      string `json:"payloadJson"`
}

type runSummary struct {
	RunID       string `json:"runId"`
	ProjectID   string `json:"projectId"`
	AgentName   string `json:"agentName"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
}

type runDetail struct {
	Summary    runSummary `json:"summary"`
	Output     string     `json:"output"`
	ResultJSON string     `json:"resultJson"`
}

type startRunResponse struct {
	Run     runSummary `json:"run"`
	Started bool       `json:"started"`
}

type getRunResponse struct {
	Run runDetail `json:"run"`
}

type stopRunResponse struct {
	Run           runDetail `json:"run"`
	StopRequested bool      `json:"stopRequested"`
}

type logChunk struct {
	Data      string      `json:"data"`
	Offset    protoUint64 `json:"offset"`
	IsFinal   bool        `json:"isFinal"`
	RunStatus string      `json:"runStatus"`
	CreatedAt string      `json:"createdAt"`
	Run       runSummary  `json:"run"`
}

type executionPayload struct {
	SchemaVersion  string `json:"schemaVersion"`
	CorrelationID  string `json:"correlationId"`
	Repository     string `json:"repository"`
	Commit         string `json:"commit"`
	TimeoutSeconds int64  `json:"timeoutSeconds"`
	NetworkEnabled bool   `json:"networkEnabled"`
}

type connectErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type connectEndStream struct {
	Error *connectErrorBody `json:"error"`
}

var _ execution.Adapter = (*Client)(nil)

// Start validates the immutable repository identity before creating an
// asynchronous AC run. The commit and resource policy are duplicated into the
// structured payload so the Agent prompt cannot silently redefine them.
func (c *Client) Start(ctx context.Context, request execution.Request) (execution.Run, error) {
	if err := validateExecutionRequest(request); err != nil {
		return execution.Run{}, err
	}
	payload, err := json.Marshal(executionPayload{
		SchemaVersion: "v1", CorrelationID: request.CorrelationID,
		Repository: request.Repository, Commit: request.CommitSHA,
		TimeoutSeconds: int64(request.Timeout / time.Second),
		NetworkEnabled: request.Policy.NetworkEnabled,
	})
	if err != nil {
		return execution.Run{}, fmt.Errorf("%w: encode payload", ErrRejected)
	}
	cleanupPolicy := "RUN_SANDBOX_CLEANUP_POLICY_STOP_ON_COMPLETION"
	if request.Policy.Cleanup == "remove" {
		cleanupPolicy = "RUN_SANDBOX_CLEANUP_POLICY_REMOVE_ON_COMPLETION"
	}
	wireRequest := struct {
		Run runAgentRequest `json:"run"`
	}{Run: runAgentRequest{
		ProjectID: request.ProjectID, AgentName: request.AgentName,
		Prompt: immutablePrompt(request), Source: "RUN_SOURCE_API",
		CleanupPolicy: cleanupPolicy, OutputSchemaJSON: diagnosticOutputSchema,
		ClientRequestID: request.CorrelationID, Driver: request.Policy.Driver,
		PayloadJSON: string(payload),
	}}
	var response startRunResponse
	if err := c.doUnary(ctx, startRunProcedure, wireRequest, &response); err != nil {
		return execution.Run{}, err
	}
	if strings.TrimSpace(response.Run.RunID) == "" {
		return execution.Run{}, fmt.Errorf("%w: StartRun response omitted runId", ErrMalformedResponse)
	}
	if !response.Started && !isTerminalStatus(response.Run.Status) {
		return execution.Run{}, fmt.Errorf("%w: AC did not start run", ErrRejected)
	}
	c.deadlines.Store(response.Run.RunID, time.Now().Add(request.Timeout))
	return execution.Run{
		ID: response.Run.RunID, CorrelationID: request.CorrelationID,
		StartedAt: parseTime(response.Run.StartedAt),
	}, nil
}

// Events follows AC's byte-offset log stream. Offsets are exposed as sequence
// values so a caller can reconnect without replaying already delivered output.
func (c *Client) Events(ctx context.Context, runID string, after uint64) (<-chan execution.Event, <-chan error) {
	events := make(chan execution.Event)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		runID = strings.TrimSpace(runID)
		streamContext := ctx
		cancel := func() {}
		if value, ok := c.deadlines.Load(runID); ok {
			streamContext, cancel = context.WithDeadline(ctx, value.(time.Time))
		}
		defer cancel()
		err := c.followEvents(streamContext, runID, after, events)
		if errors.Is(err, ErrTimeout) {
			stopContext, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = c.Cancel(stopContext, runID, "RepoMender execution deadline expired")
			stopCancel()
		}
		if err == nil || errors.Is(err, ErrTimeout) || errors.Is(err, ErrCancelled) {
			c.deadlines.Delete(runID)
		}
		errs <- err
	}()
	return events, errs
}

func (c *Client) followEvents(ctx context.Context, runID string, after uint64, events chan<- execution.Event) error {
	if runID == "" {
		return fmt.Errorf("%w: run ID is required", ErrRejected)
	}
	wireRequest := struct {
		RunID           string      `json:"runId"`
		StartOffset     protoUint64 `json:"startOffset"`
		Follow          bool        `json:"follow"`
		IncludeMetadata bool        `json:"includeMetadata"`
	}{
		RunID: runID, StartOffset: protoUint64(after), Follow: true, IncludeMetadata: true,
	}
	body, err := json.Marshal(wireRequest)
	if err != nil {
		return fmt.Errorf("%w: encode log request", ErrRejected)
	}
	framed := encodeConnectFrame(0, body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+followRunLogsProcedure, bytes.NewReader(framed))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	c.setHeaders(request, connectStreamingJSON)
	response, err := c.streamClient.Do(request)
	if err != nil {
		return c.mapTransportError(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return c.decodeHTTPError(response)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), connectStreamingJSON) {
		return fmt.Errorf("%w: unexpected stream content type %q", ErrMalformedResponse, response.Header.Get("Content-Type"))
	}

	terminal := false
	for {
		flag, payload, err := readConnectFrame(response.Body)
		if err != nil && ctx.Err() != nil {
			return c.mapTransportError(ctx.Err())
		}
		if errors.Is(err, io.EOF) {
			if terminal {
				return nil
			}
			return fmt.Errorf("%w: stream ended before terminal event", ErrStreamDropped)
		}
		if err != nil {
			return err
		}
		if flag&connectEndStreamFlag != 0 {
			var end connectEndStream
			if len(payload) != 0 && json.Unmarshal(payload, &end) != nil {
				return fmt.Errorf("%w: invalid end-stream envelope", ErrMalformedResponse)
			}
			if end.Error != nil {
				return mapConnectCode(end.Error.Code, end.Error.Message)
			}
			if terminal {
				return nil
			}
			return fmt.Errorf("%w: end-stream arrived before terminal event", ErrStreamDropped)
		}
		if flag != 0 {
			return fmt.Errorf("%w: unsupported Connect frame flag %d", ErrMalformedResponse, flag)
		}
		var chunk logChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return fmt.Errorf("%w: decode log chunk: %v", ErrMalformedResponse, err)
		}
		event := normalizeLogChunk(runID, chunk, c.redactor)
		if chunk.Data == "" && !chunk.IsFinal && chunk.Run.Status == "" {
			continue
		}
		if chunk.IsFinal {
			terminal = true
		}
		select {
		case events <- event:
		case <-ctx.Done():
			return c.mapTransportError(ctx.Err())
		}
	}
}

func (c *Client) Cancel(ctx context.Context, runID, reason string) error {
	runID = strings.TrimSpace(runID)
	reason = strings.TrimSpace(reason)
	if runID == "" || reason == "" || len(reason) > 512 {
		return fmt.Errorf("%w: run ID and bounded reason are required", ErrRejected)
	}
	var response stopRunResponse
	if err := c.doUnary(ctx, stopRunProcedure, map[string]string{"runId": runID, "reason": reason}, &response); err != nil {
		return err
	}
	if !response.StopRequested && normalizeStatus(response.Run.Summary.Status) != "cancelled" {
		return fmt.Errorf("%w: AC did not accept cancellation", ErrRejected)
	}
	return nil
}

func (c *Client) Result(ctx context.Context, runID string) (execution.Result, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return execution.Result{}, fmt.Errorf("%w: run ID is required", ErrRejected)
	}
	var response getRunResponse
	if err := c.doUnary(ctx, getRunProcedure, map[string]string{"runId": runID}, &response); err != nil {
		return execution.Result{}, err
	}
	if response.Run.Summary.RunID == "" {
		return execution.Result{}, fmt.Errorf("%w: GetRun response omitted run", ErrMalformedResponse)
	}
	status := normalizeStatus(response.Run.Summary.Status)
	if !isTerminalStatus(response.Run.Summary.Status) {
		return execution.Result{}, fmt.Errorf("%w: run is not terminal", ErrRejected)
	}
	if status == "canceled" {
		return execution.Result{}, fmt.Errorf("%w: run was cancelled", ErrCancelled)
	}
	if status == "failed" {
		failure := strings.ToLower(response.Run.Summary.Error)
		if strings.Contains(failure, "sandbox") {
			return execution.Result{}, fmt.Errorf("%w: terminal run failed", ErrSandboxFailed)
		}
		return execution.Result{}, fmt.Errorf("%w: terminal run failed", ErrAgentFailed)
	}
	// AC's resultJson is runtime metadata for the provider, while output is the
	// agent's textual result. Prefer output and retain resultJson as a legacy
	// fallback for older daemons that returned the structured result there.
	output := []byte(response.Run.Output)
	if len(bytes.TrimSpace(output)) == 0 {
		output = []byte(response.Run.ResultJSON)
	}
	envelope, schemaVersion, ok := extractResultEnvelope(output)
	if !ok {
		return execution.Result{}, fmt.Errorf("%w: terminal result does not match schema envelope", ErrMalformedResponse)
	}
	return execution.Result{
		SchemaVersion: schemaVersion, RunID: runID, Status: status,
		Output:     json.RawMessage(c.redactor.Redact(string(envelope))),
		FinishedAt: parseTime(response.Run.Summary.CompletedAt),
	}, nil
}

// extractResultEnvelope tolerates provider/runtime preambles while accepting
// only a top-level JSON object that contains the required RepoMender fields.
// This keeps diagnostic warnings from AC out of the persisted result without
// weakening the adapter's schema-envelope check.
func extractResultEnvelope(output []byte) (json.RawMessage, string, bool) {
	for start := bytes.IndexByte(output, '{'); start >= 0; {
		decoder := json.NewDecoder(bytes.NewReader(output[start:]))
		var raw json.RawMessage
		if decoder.Decode(&raw) == nil {
			var envelope struct {
				SchemaVersion string `json:"schemaVersion"`
				Summary       string `json:"summary"`
			}
			if json.Unmarshal(raw, &envelope) == nil && envelope.SchemaVersion != "" && envelope.Summary != "" {
				return raw, envelope.SchemaVersion, true
			}
		}
		next := bytes.IndexByte(output[start+1:], '{')
		if next < 0 {
			break
		}
		start += next + 1
	}
	return nil, "", false
}

func (c *Client) doUnary(ctx context.Context, procedure string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("%w: encode request", ErrRejected)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+procedure, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	c.setHeaders(request, connectUnaryJSON)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return c.mapTransportError(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return c.decodeHTTPError(response)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedResponse, err)
	}
	return nil
}

func (c *Client) setHeaders(request *http.Request, contentType string) {
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", contentType)
	request.Header.Set("Connect-Protocol-Version", "1")
	if c.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.authToken)
	}
}

func (c *Client) decodeHTTPError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var connectError connectErrorBody
	if json.Unmarshal(body, &connectError) == nil && connectError.Code != "" {
		return mapConnectCode(connectError.Code, connectError.Message)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: HTTP %d", ErrRejected, response.StatusCode)
	}
	if response.StatusCode == http.StatusGatewayTimeout {
		return fmt.Errorf("%w: HTTP %d", ErrTimeout, response.StatusCode)
	}
	return fmt.Errorf("%w: HTTP %d", ErrUnavailable, response.StatusCode)
}

func (c *Client) mapTransportError(err error) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", ErrCancelled, err)
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout") {
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	}
	return fmt.Errorf("%w: %v", ErrUnavailable, err)
}

func mapConnectCode(code, message string) error {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "canceled":
		return fmt.Errorf("%w: %s", ErrCancelled, message)
	case "deadline_exceeded":
		return fmt.Errorf("%w: %s", ErrTimeout, message)
	case "unavailable", "resource_exhausted":
		return fmt.Errorf("%w: %s", ErrUnavailable, message)
	case "unauthenticated", "permission_denied", "invalid_argument", "not_found", "already_exists":
		return fmt.Errorf("%w: %s", ErrRejected, message)
	case "failed_precondition", "unimplemented":
		return fmt.Errorf("%w: %s", ErrIncompatible, message)
	default:
		return fmt.Errorf("%w: %s", ErrAgentFailed, message)
	}
}

func validateExecutionRequest(request execution.Request) error {
	if strings.TrimSpace(request.CorrelationID) == "" || strings.TrimSpace(request.ProjectID) == "" ||
		strings.TrimSpace(request.AgentName) == "" || strings.TrimSpace(request.Repository) == "" ||
		strings.TrimSpace(request.Prompt) == "" || request.Timeout <= 0 {
		return fmt.Errorf("%w: correlation, project, agent, repository, prompt, and timeout are required", ErrRejected)
	}
	commit := strings.TrimSpace(request.CommitSHA)
	if (len(commit) != 40 && len(commit) != 64) || !isHex(commit) {
		return fmt.Errorf("%w: commit must be an immutable full Git object ID", ErrRejected)
	}
	if request.Timeout > 2*time.Hour {
		return fmt.Errorf("%w: timeout exceeds two-hour limit", ErrRejected)
	}
	return nil
}

func immutablePrompt(request execution.Request) string {
	return fmt.Sprintf(
		"RepoMender immutable execution context:\nRepository: %s\nCommit: %s\n"+
			"Do not change, merge, or push any branch.\n\nTask:\n%s",
		request.Repository, request.CommitSHA, request.Prompt,
	)
}

func isHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

func normalizeLogChunk(runID string, chunk logChunk, redactor *redactor) execution.Event {
	status := chunk.RunStatus
	if chunk.Run.Status != "" {
		status = chunk.Run.Status
	}
	event := execution.Event{
		Sequence: uint64(chunk.Offset), RunID: runID, Kind: execution.EventLog,
		Stream: "stdout", Message: redactor.Redact(chunk.Data),
		CreatedAt: parseTime(chunk.CreatedAt),
	}
	if chunk.Data == "" {
		event.Kind = execution.EventStatus
		event.Message = normalizeStatus(status)
	}
	if chunk.IsFinal {
		event.Kind = execution.EventCompleted
		event.Message = normalizeStatus(status)
		event.Terminal = true
	}
	return event
}

func normalizeStatus(status string) string {
	return strings.ToLower(strings.TrimPrefix(status, "RUN_STATUS_"))
}

func isTerminalStatus(status string) bool {
	switch normalizeStatus(status) {
	case "succeeded", "failed", "canceled":
		return true
	default:
		return false
	}
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func encodeConnectFrame(flag byte, payload []byte) []byte {
	frame := make([]byte, 5+len(payload))
	frame[0] = flag
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	return frame
}

func readConnectFrame(reader io.Reader) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return 0, nil, err
	}
	size := binary.BigEndian.Uint32(header[1:])
	if size > maxConnectFrameBytes {
		return 0, nil, fmt.Errorf("%w: Connect frame exceeds limit", ErrMalformedResponse)
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, fmt.Errorf("%w: incomplete Connect frame", ErrStreamDropped)
	}
	return header[0], payload, nil
}

type protoUint64 uint64

func (value protoUint64) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatUint(uint64(value), 10))
}

func (value *protoUint64) UnmarshalJSON(data []byte) error {
	var text string
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
	} else {
		text = string(data)
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return err
	}
	*value = protoUint64(parsed)
	return nil
}

type redactor struct {
	values []string
}

func newRedactor(values []string) *redactor {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); len(trimmed) >= 4 {
			filtered = append(filtered, trimmed)
		}
	}
	return &redactor{values: filtered}
}

func (r *redactor) Redact(value string) string {
	for _, sensitive := range r.values {
		value = strings.ReplaceAll(value, sensitive, "[REDACTED]")
	}
	return value
}
