package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log"
	"net/http"
	"net/url"
	"os"
)

// This local-only provider fixture implements the small GitHub App and GitLab
// OAuth/REST surface used by the M2 Compose acceptance flow.

func main() {
	if len(os.Args) == 3 && os.Args[1] == "keygen" {
		if err := generatePrivateKey(os.Args[2]); err != nil {
			log.Fatal(err)
		}
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /github/apps/repomender-smoke/installations/new", func(w http.ResponseWriter, r *http.Request) {
		redirect(w, r, "http://localhost:18088/api/v1/scm/github/callback", url.Values{
			"installation_id": {"42"}, "state": {r.URL.Query().Get("state")},
		})
	})
	mux.HandleFunc("GET /github/app/installations/42", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"id": 42, "account": map[string]any{"id": 9, "login": "github-smoke"}})
	})
	mux.HandleFunc("POST /github/app/installations/42/access_tokens", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"token": "github-installation-smoke-token"})
	})
	mux.HandleFunc("GET /github/installation/repositories", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"repositories": []map[string]any{{
			"id": 701, "full_name": "smoke/github-repository",
			"clone_url":      "https://github.com/smoke/github-repository.git",
			"html_url":       "https://github.com/smoke/github-repository",
			"default_branch": "main", "visibility": "private", "language": "Go",
		}}})
	})
	mux.HandleFunc("GET /github/repos/smoke/github-repository/actions/runs/99/logs", func(w http.ResponseWriter, _ *http.Request) {
		var archive bytes.Buffer
		writer := zip.NewWriter(&archive)
		entry, err := writer.Create("ci/1.txt")
		if err != nil {
			http.Error(w, "fixture archive failed", http.StatusInternalServerError)
			return
		}
		_, _ = entry.Write([]byte("TOKEN=fixture-secret\nFAIL: expected one result, found two\n"))
		if err := writer.Close(); err != nil {
			http.Error(w, "fixture archive failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive.Bytes())
	})
	mux.HandleFunc("GET /github/repos/smoke/github-repository/issues/731", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"number": 731, "title": "Retry webhook delivery", "body": "Repair the retry path", "html_url": "https://github.com/smoke/github-repository/issues/731", "state": "open"})
	})
	mux.HandleFunc("GET /github/repos/smoke/github-repository/git/ref/heads/main", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"object": map[string]string{"sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}})
	})
	mux.HandleFunc("POST /github/repos/smoke/github-repository/git/refs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ref": "refs/heads/repomender/repair/smoke", "object": map[string]string{"sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}})
	})
	mux.HandleFunc("POST /github/repos/smoke/github-repository/git/blobs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"sha": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"})
	})
	mux.HandleFunc("POST /github/repos/smoke/github-repository/git/trees", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"sha": "cccccccccccccccccccccccccccccccccccccccc"})
	})
	mux.HandleFunc("POST /github/repos/smoke/github-repository/git/commits", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"sha": "dddddddddddddddddddddddddddddddddddddddd"})
	})
	mux.HandleFunc("/github/repos/smoke/github-repository/git/refs/heads/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"object": map[string]string{"sha": "dddddddddddddddddddddddddddddddddddddddd"}})
	})
	mux.HandleFunc("POST /github/repos/smoke/github-repository/pulls", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"number": 7310, "html_url": "https://github.com/smoke/github-repository/pull/7310", "draft": true})
	})
	mux.HandleFunc("POST /github/repos/smoke/github-repository/check-runs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"id": 9901, "name": "RepoMender CI Diagnosis", "status": "completed"})
	})
	mux.HandleFunc("GET /gitlab/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		redirect(w, r, r.URL.Query().Get("redirect_uri"), url.Values{
			"code": {"gitlab-smoke-code"}, "state": {r.URL.Query().Get("state")},
		})
	})
	mux.HandleFunc("POST /gitlab/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"access_token": "gitlab-smoke-access", "refresh_token": "gitlab-smoke-refresh",
			"expires_in": 3600,
		})
	})
	mux.HandleFunc("GET /gitlab/api/v4/user", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"id": 12, "username": "gitlab-smoke", "name": "GitLab Smoke"})
	})
	mux.HandleFunc("GET /gitlab/api/v4/projects", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []map[string]any{{
			"id": 801, "path_with_namespace": "smoke/gitlab-repository",
			"http_url_to_repo": "https://gitlab.com/smoke/gitlab-repository.git",
			"web_url":          "https://gitlab.com/smoke/gitlab-repository",
			"default_branch":   "main", "visibility": "private",
		}})
	})
	log.Fatal(http.ListenAndServe(":9090", mux))
}

func generatePrivateKey(path string) error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{
		Type: "PRIVATE KEY", Bytes: encoded,
	}), 0o644)
}

func redirect(w http.ResponseWriter, r *http.Request, destination string, values url.Values) {
	target, err := url.Parse(destination)
	if err != nil || target.Scheme == "" {
		http.Error(w, "invalid fixture redirect", http.StatusBadRequest)
		return
	}
	target.RawQuery = values.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
