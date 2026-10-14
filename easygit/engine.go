package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxFileSize   = 95 << 20 // GitHub rejects blobs above 100 MB
	inlineLimit   = 1 << 20  // text files up to this size ride inside the tree request
	inlineBudget  = 8 << 20  // total inline text per commit
	workers       = 4
	defaultMode   = "100644"
	conflictLabel = ".conflict-"
)

type SyncResult struct {
	Pulled    int // files downloaded
	Removed   int // local files removed because they were deleted on GitHub
	Pushed    int // files added or changed in the new commit
	Deleted   int // files deleted in the new commit
	Conflicts []string
	Commit    string
}

func (r SyncResult) Nothing() bool {
	return r.Pulled == 0 && r.Removed == 0 && r.Pushed == 0 && r.Deleted == 0
}

func (r SyncResult) String() string {
	if r.Nothing() {
		return "Already up to date"
	}
	var p []string
	if r.Pulled+r.Removed > 0 {
		p = append(p, fmt.Sprintf("pulled %d", r.Pulled+r.Removed))
	}
	if r.Pushed+r.Deleted > 0 {
		s := fmt.Sprintf("committed %d", r.Pushed+r.Deleted)
		if len(r.Commit) >= 7 {
			s += " (" + r.Commit[:7] + ")"
		}
		p = append(p, s)
	}
	if len(r.Conflicts) > 0 {
		p = append(p, fmt.Sprintf("%d conflict(s), your copy saved as *.conflict-*", len(r.Conflicts)))
	}
	return strings.Join(p, ", ")
}

type Engine struct {
	Folder string
	Owner  string
	Repo   string
	Branch string
	Log    func(string)

	gh    *GitHub
	state *State
	mu    sync.Mutex // one sync at a time
}

func NewEngine(folder, owner, repo, branch, token string, logf func(string)) *Engine {
	if logf == nil {
		logf = func(string) {}
	}
	e := &Engine{Folder: folder, Owner: owner, Repo: repo, Branch: branch, Log: logf, gh: NewGitHub(token, owner, repo)}
	return e
}

func (e *Engine) fullName() string { return e.Owner + "/" + e.Repo }

type localFile struct {
	abs   string
	sha   string
	size  int64
	mtime int64
}

// Sync pulls whatever changed on GitHub, then commits local changes as a single
// commit. Only changed files travel in either direction.
func (e *Engine) Sync(message string) (SyncResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.Branch == "" {
		info, err := e.gh.Repo()
		if err != nil {
			return SyncResult{}, err
		}
		e.Branch = info.DefaultBranch
	}
	e.state = LoadState(e.Folder, e.fullName(), e.Branch)

	var total SyncResult
	for attempt := 0; attempt < 4; attempt++ {
		res, err := e.syncOnce(message)
		total.Pulled += res.Pulled
		total.Removed += res.Removed
		total.Conflicts = append(total.Conflicts, res.Conflicts...)
		if err == nil {
			total.Pushed, total.Deleted, total.Commit = res.Pushed, res.Deleted, res.Commit
			return total, nil
		}
		if errors.Is(err, ErrNotFastForward) || errors.Is(err, errRetry) {
			e.Log("GitHub changed while syncing, retrying…")
			continue
		}
		return total, err
	}
	return total, errors.New("GitHub keeps changing, try again in a moment")
}

var errRetry = errors.New("retry")

func (e *Engine) syncOnce(message string) (SyncResult, error) {
	var res SyncResult
	st := e.state

	head, err := e.gh.Ref(e.Branch)
	if errors.Is(err, ErrBranchMissing) {
		return res, e.initEmptyRepo(message)
	}
	if err != nil {
		return res, err
	}

	// 1. What does GitHub have? Only re-list the tree when the branch moved.
	if head != st.Head || st.Tree == "" {
		tree, err := e.gh.CommitTree(head)
		if err != nil {
			return res, err
		}
		entries, err := e.gh.Tree(tree)
		if err != nil {
			return res, err
		}
		remote := make(map[string]Entry, len(entries))
		for _, t := range entries {
			if t.Type == "blob" {
				remote[t.Path] = Entry{SHA: t.SHA, Mode: t.Mode}
			}
		}
		// The previous base stays in oldBase for the three-way comparison below.
		oldBase := st.Base
		st.Head, st.Tree = head, tree
		return e.reconcile(res, oldBase, remote, message)
	}
	return e.reconcile(res, st.Base, st.Base, message)
}

func (e *Engine) reconcile(res SyncResult, base, remote map[string]Entry, message string) (SyncResult, error) {
	st := e.state
	local, err := e.scanLocal(remote)
	if err != nil {
		return res, err
	}

	var downloads []string
	var removals []string
	pushes := map[string]*localFile{}
	var deletes []string
	var conflicts []string

	paths := map[string]struct{}{}
	for p := range base {
		paths[p] = struct{}{}
	}
	for p := range remote {
		paths[p] = struct{}{}
	}
	for p := range local {
		paths[p] = struct{}{}
	}
	for p := range paths {
		b, r := base[p].SHA, remote[p].SHA
		l := ""
		if lf := local[p]; lf != nil {
			l = lf.sha
		}
		switch {
		case l == r:
		case l == b: // only GitHub changed
			if r == "" {
				removals = append(removals, p)
			} else {
				downloads = append(downloads, p)
			}
		case r == b: // only we changed
			if l == "" {
				deletes = append(deletes, p)
			} else {
				pushes[p] = local[p]
			}
		default: // both changed
			switch {
			case r == "":
				pushes[p] = local[p] // deleted remotely but edited here: keep ours
			case l == "":
				downloads = append(downloads, p) // deleted here but edited remotely: take theirs
			default:
				conflicts = append(conflicts, p)
				downloads = append(downloads, p)
			}
		}
	}
	sort.Strings(downloads)
	sort.Strings(removals)
	sort.Strings(deletes)

	// 2. Apply GitHub's changes locally.
	for _, p := range conflicts {
		backup := conflictName(p)
		if err := os.Rename(e.abs(p), e.abs(backup)); err != nil {
			return res, fmt.Errorf("cannot back up %s: %w", p, err)
		}
		delete(st.Cache, p)
		e.Log("Conflict: " + p + " (your version kept as " + backup + ")")
	}
	res.Conflicts = conflicts
	if err := e.download(downloads, remote); err != nil {
		return res, err
	}
	res.Pulled = len(downloads)
	for _, p := range removals {
		_ = os.Remove(e.abs(p))
		e.pruneEmptyDirs(filepath.Dir(e.abs(p)))
		delete(st.Cache, p)
	}
	res.Removed = len(removals)
	if len(downloads)+len(removals) > 0 {
		e.Log(fmt.Sprintf("Downloaded %d file(s), removed %d", len(downloads), len(removals)))
	}

	// GitHub's side is now mirrored locally; remember it before pushing so a failed
	// push does not make us download the same files again.
	st.Base = remote
	if err := st.Save(e.Folder, e.fullName(), e.Branch); err != nil {
		return res, err
	}

	// 3. Commit local changes.
	if len(pushes)+len(deletes) == 0 {
		return res, nil
	}
	commit, err := e.push(pushes, deletes, remote, message)
	if err != nil {
		return res, err
	}
	res.Pushed, res.Deleted, res.Commit = len(pushes), len(deletes), commit
	return res, nil
}

func (e *Engine) push(pushes map[string]*localFile, deletes []string, remote map[string]Entry, message string) (string, error) {
	st := e.state
	names := make([]string, 0, len(pushes))
	for p := range pushes {
		names = append(names, p)
	}
	sort.Strings(names)

	type pending struct {
		path    string
		content []byte
	}
	var blobs []pending
	var entries []map[string]any
	inlineUsed := 0
	newBase := make(map[string]Entry, len(remote)+len(names))
	for k, v := range remote {
		newBase[k] = v
	}

	var added, updated []string
	for _, p := range names {
		lf := pushes[p]
		data, err := os.ReadFile(lf.abs)
		if err != nil {
			return "", fmt.Errorf("cannot read %s: %w", p, err)
		}
		sum := blobSHA(data) // re-hash: the file may have changed since the scan
		lf.sha = sum
		mode := remote[p].Mode
		if mode == "" {
			mode = defaultMode
		}
		if prev, ok := remote[p]; ok && prev.SHA == sum {
			continue
		} else if ok {
			updated = append(updated, p)
		} else {
			added = append(added, p)
		}
		newBase[p] = Entry{SHA: sum, Mode: mode}
		if len(data) <= inlineLimit && inlineUsed+len(data) <= inlineBudget && utf8.Valid(data) && !bytes.Contains(data, []byte{0}) {
			inlineUsed += len(data)
			entries = append(entries, map[string]any{"path": p, "mode": mode, "type": "blob", "content": string(data)})
		} else {
			blobs = append(blobs, pending{p, data})
			entries = append(entries, map[string]any{"path": p, "mode": mode, "type": "blob", "sha": nil})
		}
	}
	for _, p := range deletes {
		mode := remote[p].Mode
		if mode == "" {
			mode = defaultMode
		}
		entries = append(entries, map[string]any{"path": p, "mode": mode, "type": "blob", "sha": nil})
		delete(newBase, p)
	}
	if len(entries) == 0 {
		return "", nil
	}

	// Binary / large files go up as blobs first, a few at a time.
	if len(blobs) > 0 {
		e.Log(fmt.Sprintf("Uploading %d binary/large file(s)…", len(blobs)))
		shas := make(map[string]string, len(blobs))
		var mu sync.Mutex
		if err := parallel(len(blobs), func(i int) error {
			sha, err := e.gh.CreateBlob(blobs[i].content)
			if err != nil {
				return fmt.Errorf("upload %s: %w", blobs[i].path, err)
			}
			mu.Lock()
			shas[blobs[i].path] = sha
			mu.Unlock()
			return nil
		}); err != nil {
			return "", err
		}
		for _, en := range entries {
			if s, ok := shas[en["path"].(string)]; ok {
				en["sha"] = s
			}
		}
	}

	if message == "" {
		message = autoMessage(added, updated, deletes)
	}
	tree, err := e.gh.CreateTree(st.Tree, entries)
	if err != nil {
		return "", err
	}
	commit, err := e.gh.CreateCommit(message, tree, st.Head)
	if err != nil {
		return "", err
	}
	if err := e.gh.UpdateRef(e.Branch, commit); err != nil {
		return "", err
	}
	st.Head, st.Tree, st.Base = commit, tree, newBase
	for p, lf := range pushes {
		if info, err := os.Stat(lf.abs); err == nil {
			st.Cache[p] = FileCache{MTime: info.ModTime().UnixNano(), Size: info.Size(), SHA: lf.sha}
		}
	}
	for _, p := range deletes {
		delete(st.Cache, p)
	}
	if err := st.Save(e.Folder, e.fullName(), e.Branch); err != nil {
		return commit, err
	}
	e.Log("Committed: " + firstLine(message))
	return commit, nil
}

// initEmptyRepo creates the very first commit. Git's object API refuses empty
// repositories, so the contents API is used for a single file, and the next pass
// pushes the rest normally.
func (e *Engine) initEmptyRepo(message string) error {
	info, err := e.gh.Repo()
	if err != nil {
		return err
	}
	if e.Branch != info.DefaultBranch {
		// A branch that does not exist yet in a repo that already has commits.
		if _, err := e.gh.Ref(info.DefaultBranch); err == nil {
			return fmt.Errorf("branch %q does not exist in %s", e.Branch, e.fullName())
		}
		e.Branch = info.DefaultBranch
		e.state = LoadState(e.Folder, e.fullName(), e.Branch)
	}
	local, err := e.scanLocal(nil)
	if err != nil {
		return err
	}
	if len(local) == 0 {
		return errors.New("the repository is empty and the folder has no files to upload")
	}
	var first string
	for p, lf := range local {
		if first == "" || lf.size < local[first].size {
			first = p
		}
	}
	data, err := os.ReadFile(local[first].abs)
	if err != nil {
		return err
	}
	if message == "" {
		message = "EasyGit: initial commit"
	}
	e.Log("Repository is empty, creating the first commit…")
	if _, err := e.gh.PutFile(escapePath(first), data, message, ""); err != nil {
		return err
	}
	return errRetry
}

// ---- local scanning ---------------------------------------------------------------

func (e *Engine) abs(rel string) string { return filepath.Join(e.Folder, filepath.FromSlash(rel)) }

func (e *Engine) scanLocal(remote map[string]Entry) (map[string]*localFile, error) {
	ig := LoadIgnore(e.Folder)
	remoteDirs := map[string]bool{}
	for p := range remote {
		for d := path0(p); d != ""; d = path0(d) {
			remoteDirs[d] = true
		}
	}
	out := map[string]*localFile{}
	cache := e.state.Cache
	seen := map[string]bool{}

	err := filepath.WalkDir(e.Folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == e.Folder {
				return err
			}
			return nil // unreadable entry: skip
		}
		rel, rerr := filepath.Rel(e.Folder, p)
		if rerr != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if builtinIgnored(rel) || (ig.Match(rel, true) && !remoteDirs[rel]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || builtinIgnored(rel) {
			return nil
		}
		if _, tracked := remote[rel]; !tracked && ig.Match(rel, false) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > maxFileSize {
			e.Log("Skipped (over 95 MB): " + rel)
			return nil
		}
		lf := &localFile{abs: p, size: info.Size(), mtime: info.ModTime().UnixNano()}
		if c, ok := cache[rel]; ok && c.MTime == lf.mtime && c.Size == lf.size {
			lf.sha = c.SHA
		} else {
			sha, err := hashFile(p, lf.size)
			if err != nil {
				return nil // locked or changing right now; picked up next time
			}
			lf.sha = sha
			cache[rel] = FileCache{MTime: lf.mtime, Size: lf.size, SHA: sha}
		}
		seen[rel] = true
		out[rel] = lf
		return nil
	})
	for rel := range cache {
		if !seen[rel] {
			delete(cache, rel)
		}
	}
	return out, err
}

func path0(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return ""
	}
	return p[:i]
}

func blobSHA(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func hashFile(path string, size int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", size)
	n, err := io.Copy(h, f)
	if err != nil {
		return "", err
	}
	if n != size {
		return "", errors.New("file changed while hashing")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ---- downloading ------------------------------------------------------------------

func (e *Engine) download(paths []string, remote map[string]Entry) error {
	if len(paths) == 0 {
		return nil
	}
	var mu sync.Mutex
	return parallel(len(paths), func(i int) error {
		p := paths[i]
		want := remote[p].SHA
		data, err := e.gh.Blob(want)
		if err != nil {
			return fmt.Errorf("download %s: %w", p, err)
		}
		if got := blobSHA(data); got != want {
			return fmt.Errorf("download %s: checksum mismatch", p)
		}
		dst := e.abs(p)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		tmp := dst + ".easygit-tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, dst); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("write %s: %w", p, err)
		}
		if info, err := os.Stat(dst); err == nil {
			mu.Lock()
			e.state.Cache[p] = FileCache{MTime: info.ModTime().UnixNano(), Size: info.Size(), SHA: want}
			mu.Unlock()
		}
		return nil
	})
}

func (e *Engine) pruneEmptyDirs(dir string) {
	root := filepath.Clean(e.Folder)
	for dir = filepath.Clean(dir); dir != root && strings.HasPrefix(dir, root); dir = filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil { // only succeeds when empty
			return
		}
	}
}

// ---- helpers ----------------------------------------------------------------------

func parallel(n int, fn func(i int) error) error {
	idx := make(chan int)
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	for w := 0; w < workers && w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				if err := fn(i); err != nil {
					once.Do(func() { first = err })
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		idx <- i
	}
	close(idx)
	wg.Wait()
	return first
}

func conflictName(rel string) string {
	ext := filepath.Ext(rel)
	return strings.TrimSuffix(rel, ext) + conflictLabel + time.Now().Format("20060102-150405") + ext
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func autoMessage(added, updated, deleted []string) string {
	var parts []string
	add := func(verb string, list []string) {
		if len(list) == 0 {
			return
		}
		names := make([]string, 0, 3)
		for i, p := range list {
			if i == 3 {
				break
			}
			names = append(names, filepath.Base(p))
		}
		s := verb + " " + strings.Join(names, ", ")
		if len(list) > 3 {
			s += fmt.Sprintf(" (+%d more)", len(list)-3)
		}
		parts = append(parts, s)
	}
	add("Add", added)
	add("Update", updated)
	add("Delete", deleted)
	return "EasyGit: " + strings.Join(parts, "; ")
}
