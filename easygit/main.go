package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
)

func main() {
	runtime.LockOSThread() // the Win32 UI must stay on one thread
	for _, a := range os.Args[1:] {
		if a == "--cli" {
			os.Exit(runCLI(os.Args[1:]))
		}
	}
	runGUI()
}

// Headless mode, handy for scripts and testing:
//
//	easygit --cli --dir C:\proj --repo owner/name [--branch main] [--msg "text"] [--token-env GH_TOKEN]
//
// Without --token-env the token saved by the GUI is used.
func runCLI(args []string) int {
	fs := flag.NewFlagSet("easygit", flag.ExitOnError)
	fs.Bool("cli", true, "headless mode")
	dir := fs.String("dir", "", "project folder")
	repo := fs.String("repo", "", "owner/name or GitHub URL")
	branch := fs.String("branch", "", "branch (default: repository default)")
	msg := fs.String("msg", "", "commit message")
	tokenEnv := fs.String("token-env", "", "environment variable holding a GitHub token")
	_ = fs.Parse(args)

	token := LoadToken()
	if *tokenEnv != "" {
		token = os.Getenv(*tokenEnv)
	}
	owner, name, ok := SplitRepo(*repo)
	if !ok || *dir == "" || token == "" {
		fmt.Fprintln(os.Stderr, "usage: easygit --cli --dir <folder> --repo <owner/name> [--branch b] [--msg m] [--token-env VAR]")
		return 2
	}
	e := NewEngine(strings.TrimSpace(*dir), owner, name, *branch, token, func(s string) { fmt.Println(" ", s) })
	res, err := e.Sync(*msg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Println(res.String())
	return 0
}
