package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const apiBase = "https://api.github.com"

// ErrNotFastForward means the branch moved while we were preparing a commit.
var ErrNotFastForward = errors.New("branch moved on GitHub")

// ErrBranchMissing means the repo exists but the branch does not (or the repo is empty).
var ErrBranchMissing = errors.New("branch not found")

type GitHub struct {
	token string
	owner string
	repo  string
	http  *http.Client
}

func NewGitHub(token, owner, repo string) *GitHub {
	return &GitHub{token: token, owner: owner, repo: repo, http: &http.Client{Timeout: 120 * time.Second}}
}

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return fmt.Sprintf("GitHub %d: %s", e.Status, e.Message) }

func (g *GitHub) repoPath(format string, a ...any) string {
	return "/repos/" + g.owner + "/" + g.repo + fmt.Sprintf(format, a...)
}

// do performs a request, retrying on rate limits and transient server errors.
// It returns the response headers and fills out (if non-nil) from the JSON body.
func (g *GitHub) do(method, path string, body any, out any, hdr map[string]string) (http.Header, int, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, 0, err
		}
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequest(method, apiBase+path, bytes.NewReader(payload))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+g.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "EasyGit")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := g.http.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == 304 {
			return resp.Header, 304, nil
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out != nil {
				if raw, ok := out.(*[]byte); ok {
					*raw = data
				} else if err := json.Unmarshal(data, out); err != nil {
					return resp.Header, resp.StatusCode, err
				}
			}
			return resp.Header, resp.StatusCode, nil
		}

		ae := &apiError{Status: resp.StatusCode}
		var msg struct{ Message string }
		if json.Unmarshal(data, &msg) == nil && msg.Message != "" {
			ae.Message = msg.Message
		} else {
			ae.Message = strings.TrimSpace(string(data))
		}
		lastErr = ae

		limited := resp.StatusCode == 429 ||
			(resp.StatusCode == 403 && (resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0" ||
				strings.Contains(strings.ToLower(ae.Message), "rate limit")))
		if limited {
			wait := 30 * time.Second
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
				wait = time.Duration(s) * time.Second
			}
			if wait > 90*time.Second || attempt == 3 {
				return resp.Header, resp.StatusCode, fmt.Errorf("GitHub rate limit reached, try again in %s", wait.Round(time.Second))
			}
			time.Sleep(wait)
			continue
		}
		if resp.StatusCode >= 500 {
			time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
			continue
		}
		return resp.Header, resp.StatusCode, ae
	}
	return nil, 0, lastErr
}

// ---- account / repo -----------------------------------------------------------

func (g *GitHub) User() (string, error) {
	var u struct{ Login string }
	if _, _, err := g.do("GET", "/user", nil, &u, nil); err != nil {
		return "", err
	}
	return u.Login, nil
}

type RepoInfo struct {
	DefaultBranch string `json:"default_branch"`
	Permissions   struct {
		Push bool `json:"push"`
	} `json:"permissions"`
}

func (g *GitHub) Repo() (*RepoInfo, error) {
	var r RepoInfo
	if _, _, err := g.do("GET", g.repoPath(""), nil, &r, nil); err != nil {
		return nil, err
	}
	return &r, nil
}

// ---- refs ---------------------------------------------------------------------

type refResp struct {
	Object struct{ SHA string } `json:"object"`
}

// Ref returns the head commit of a branch, or ErrBranchMissing.
func (g *GitHub) Ref(branch string) (string, error) {
	var r refResp
	_, _, err := g.do("GET", g.repoPath("/git/ref/heads/%s", branch), nil, &r, nil)
	var ae *apiError
	if errors.As(err, &ae) && (ae.Status == 404 || ae.Status == 409) {
		return "", ErrBranchMissing
	}
	return r.Object.SHA, err
}

// PollRef is a conditional request: unchanged => changed=false and it does not
// count against the rate limit.
func (g *GitHub) PollRef(branch, etag string) (changed bool, newEtag string, err error) {
	var r refResp
	hdr := map[string]string{}
	if etag != "" {
		hdr["If-None-Match"] = etag
	}
	h, status, err := g.do("GET", g.repoPath("/git/ref/heads/%s", branch), nil, &r, hdr)
	if err != nil {
		return false, etag, err
	}
	if status == 304 {
		return false, etag, nil
	}
	return true, h.Get("ETag"), nil
}

func (g *GitHub) UpdateRef(branch, sha string) error {
	_, _, err := g.do("PATCH", g.repoPath("/git/refs/heads/%s", branch), map[string]any{"sha": sha, "force": false}, nil, nil)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == 422 {
		return ErrNotFastForward
	}
	return err
}

// ---- objects ------------------------------------------------------------------

func (g *GitHub) CommitTree(commitSHA string) (string, error) {
	var c struct {
		Tree struct{ SHA string } `json:"tree"`
	}
	if _, _, err := g.do("GET", g.repoPath("/git/commits/%s", commitSHA), nil, &c, nil); err != nil {
		return "", err
	}
	return c.Tree.SHA, nil
}

type TreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

func (g *GitHub) Tree(treeSHA string) ([]TreeEntry, error) {
	var t struct {
		Tree      []TreeEntry `json:"tree"`
		Truncated bool        `json:"truncated"`
	}
	if _, _, err := g.do("GET", g.repoPath("/git/trees/%s?recursive=1", treeSHA), nil, &t, nil); err != nil {
		return nil, err
	}
	if t.Truncated {
		return nil, errors.New("repository is too large for EasyGit (GitHub truncated the file list)")
	}
	return t.Tree, nil
}

// Blob downloads the raw bytes of a blob.
func (g *GitHub) Blob(sha string) ([]byte, error) {
	var raw []byte
	_, _, err := g.do("GET", g.repoPath("/git/blobs/%s", sha), nil, &raw, map[string]string{"Accept": "application/vnd.github.raw+json"})
	return raw, err
}

func (g *GitHub) CreateBlob(content []byte) (string, error) {
	var r struct{ SHA string }
	body := map[string]string{"content": base64.StdEncoding.EncodeToString(content), "encoding": "base64"}
	if _, _, err := g.do("POST", g.repoPath("/git/blobs"), body, &r, nil); err != nil {
		return "", err
	}
	return r.SHA, nil
}

// CreateTree builds a tree on top of base. Entries are maps so that "sha": null
// (delete) and inline "content" (text files, saves a request) can both be expressed.
func (g *GitHub) CreateTree(base string, entries []map[string]any) (string, error) {
	var r struct{ SHA string }
	body := map[string]any{"base_tree": base, "tree": entries}
	if _, _, err := g.do("POST", g.repoPath("/git/trees"), body, &r, nil); err != nil {
		return "", err
	}
	return r.SHA, nil
}

func (g *GitHub) CreateCommit(message, tree, parent string) (string, error) {
	var r struct{ SHA string }
	body := map[string]any{"message": message, "tree": tree, "parents": []string{parent}}
	if _, _, err := g.do("POST", g.repoPath("/git/commits"), body, &r, nil); err != nil {
		return "", err
	}
	return r.SHA, nil
}

// PutFile creates a file through the contents API (path must be URL-escaped). It is the only write call that
// works on a completely empty repository, so it is used to create the first commit.
func (g *GitHub) PutFile(path string, content []byte, message, branch string) (string, error) {
	var r struct {
		Commit struct{ SHA string } `json:"commit"`
	}
	body := map[string]any{"message": message, "content": base64.StdEncoding.EncodeToString(content)}
	if branch != "" {
		body["branch"] = branch
	}
	if _, _, err := g.do("PUT", g.repoPath("/contents/%s", path), body, &r, nil); err != nil {
		return "", err
	}
	return r.Commit.SHA, nil
}
