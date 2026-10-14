package main

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type ignoreRule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// Ignore implements a practical subset of .gitignore (patterns, negation,
// anchoring, directory-only rules and ** wildcards) from the project root file.
type Ignore struct {
	rules []ignoreRule
}

func LoadIgnore(root string) *Ignore {
	ig := &Ignore{}
	f, err := os.Open(filepath.Join(root, ".gitignore"))
	if err != nil {
		return ig
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(strings.TrimSpace(sc.Text()), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := ignoreRule{}
		if strings.HasPrefix(line, "!") {
			r.negate = true
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		anchored := strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		pat := globToRegexp(line)
		if anchored {
			pat = "^" + pat + "$"
		} else {
			pat = "^(?:.*/)?" + pat + "$"
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			continue
		}
		r.re = re
		ig.rules = append(ig.rules, r)
	}
	return ig
}

func globToRegexp(g string) string {
	var b strings.Builder
	for i := 0; i < len(g); i++ {
		switch c := g[i]; c {
		case '*':
			if i+1 < len(g) && g[i+1] == '*' {
				i++
				if i+1 < len(g) && g[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			j := strings.IndexByte(g[i:], ']')
			if j < 0 {
				b.WriteString(`\[`)
			} else {
				b.WriteString(g[i : i+j+1])
				i += j
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// Match reports whether rel (slash separated, relative to the root) is ignored.
// Parent directories are checked too, like git does.
func (ig *Ignore) Match(rel string, isDir bool) bool {
	if builtinIgnored(rel) {
		return true
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		sub := strings.Join(parts[:i+1], "/")
		dir := isDir || i < len(parts)-1
		if ig.matchOne(sub, dir) {
			return true
		}
	}
	return false
}

func (ig *Ignore) matchOne(rel string, isDir bool) bool {
	ignored := false
	for _, r := range ig.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.re.MatchString(rel) {
			ignored = !r.negate
		}
	}
	return ignored
}

// Files EasyGit never syncs, whatever .gitignore says.
func builtinIgnored(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".git" || seg == ".easygit" {
			return true
		}
	}
	base := path.Base(rel)
	if base == "Thumbs.db" || base == "desktop.ini" || strings.Contains(base, ".conflict-") || strings.HasSuffix(base, ".easygit-tmp") {
		return true
	}
	return false
}
