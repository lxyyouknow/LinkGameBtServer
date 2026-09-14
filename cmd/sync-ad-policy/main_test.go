package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

func TestRemoteLatestLeavesWorkingTreeIntact(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有Git")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	planner := filepath.Join(root, "planner")
	gitTest(t, root, "init", "--bare", remote)
	gitTest(t, root, "clone", remote, work)
	setup := func(dir string) {
		gitTest(t, dir, "config", "user.name", "test")
		gitTest(t, dir, "config", "user.email", "test@localhost")
		gitTest(t, dir, "config", "commit.gpgsign", "false")
	}
	setup(work)
	gitTest(t, work, "checkout", "-b", "main")
	file := filepath.Join(work, "deploy", "config", "ad-policy.json")
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("first remote config"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, work, "add", ".")
	gitTest(t, work, "commit", "-m", "initial")
	gitTest(t, work, "push", "-u", "origin", "main")
	gitTest(t, root, "clone", "--branch", "main", remote, planner)
	setup(planner)
	if err := os.WriteFile(filepath.Join(planner, "deploy/config/ad-policy.json"), []byte("new planner config"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, planner, "add", ".")
	gitTest(t, planner, "commit", "-m", "planner")
	gitTest(t, planner, "push")
	if err := os.WriteFile(file, []byte("uncommitted local config"), 0600); err != nil {
		t.Fatal(err)
	}
	before := gitTest(t, work, "rev-parse", "HEAD")
	data, revision, err := sourceConfig(work, false)
	if err != nil || string(data) != "new planner config" {
		t.Fatalf("remote config: %q %v", data, err)
	}
	if strings.TrimSpace(gitTest(t, planner, "rev-parse", "HEAD")) != revision {
		t.Fatal("没有锁定策划提交")
	}
	local, _ := os.ReadFile(file)
	if string(local) != "uncommitted local config" || gitTest(t, work, "rev-parse", "HEAD") != before {
		t.Fatal("覆盖了本地修改/分支")
	}
	data, _, err = sourceConfig(work, true)
	if err != nil || string(data) != string(local) {
		t.Fatal("本地显式模式失效")
	}
}

func TestRemoteAtomicFailureAndNoop(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("本地没有Python3；远端仍需Python3")
	}
	cmd := exec.Command(py, "-B", "test_remote.py")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("远端文件同步测试: %v %s", err, out)
	}
}

func TestQuoteShell(t *testing.T) {
	if quoteShell("don't") != "'don'\"'\"'t'" {
		t.Fatal("shell 参数未正确引用")
	}
}
