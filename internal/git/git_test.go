package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoRootFromCommonGitDirHandlesForwardSlashPath(t *testing.T) {
	root, ok := repoRootFromCommonGitDir("C:/Users/runner/AppData/Local/Temp/repo/.git")
	if !ok {
		t.Fatal("expected .git common dir to resolve to a repo root")
	}

	want := filepath.Clean(filepath.FromSlash("C:/Users/runner/AppData/Local/Temp/repo"))
	if root != want {
		t.Fatalf("expected repo root %q, got %q", want, root)
	}
}

func TestGetDefaultBranchFromDetachedLinkedWorktreeUsesMainRepoHead(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	wtPath := filepath.Join(base, "worktree")

	mustGit(t, "", "init", "--initial-branch=main", repoDir)
	mustGit(t, repoDir, "config", "user.email", "test@test.com")
	mustGit(t, repoDir, "config", "user.name", "Test")
	mustGit(t, repoDir, "config", "init.defaultBranch", "wrong")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repoDir, "add", ".")
	mustGit(t, repoDir, "commit", "-m", "initial")
	mustGit(t, repoDir, "worktree", "add", "--detach", wtPath, "main")

	branch, err := GetDefaultBranch(wtPath)
	if err != nil {
		t.Fatalf("GetDefaultBranch failed: %v", err)
	}
	if branch != "main" {
		t.Fatalf("expected default branch main from main repo HEAD, got %q", branch)
	}
}

func TestFindMainRepoRootFromLinkedWorktree(t *testing.T) {
	base := t.TempDir()
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(base, "repo")
	wtPath := filepath.Join(base, "worktree")

	mustGit(t, "", "init", "--initial-branch=main", repoDir)
	mustGit(t, repoDir, "config", "user.email", "test@test.com")
	mustGit(t, repoDir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repoDir, "add", ".")
	mustGit(t, repoDir, "commit", "-m", "initial")
	mustGit(t, repoDir, "worktree", "add", "--detach", wtPath, "main")

	mainRoot, err := FindMainRepoRootFrom(wtPath)
	if err != nil {
		t.Fatalf("FindMainRepoRootFrom failed: %v", err)
	}
	if mainRoot != repoDir {
		t.Fatalf("expected main repo root %s, got %s", repoDir, mainRoot)
	}
}

func TestRemoveCleanWorktreeRejectsDirtyWorktree(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	wtPath := filepath.Join(base, "worktree")

	mustGit(t, "", "init", "--initial-branch=main", repoDir)
	mustGit(t, repoDir, "config", "user.email", "test@test.com")
	mustGit(t, repoDir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repoDir, "add", ".")
	mustGit(t, repoDir, "commit", "-m", "initial")
	mustGit(t, repoDir, "worktree", "add", "--detach", wtPath, "main")

	dirtyPath := filepath.Join(wtPath, "uncommitted.txt")
	if err := os.WriteFile(dirtyPath, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveCleanWorktree(repoDir, wtPath); err == nil {
		t.Fatal("expected clean worktree removal to reject dirty worktree")
	}
	if _, err := os.Stat(dirtyPath); err != nil {
		t.Fatalf("expected dirty worktree to remain: %v", err)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestResetWorktreeRecursesSubmodules(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	super := filepath.Join(base, "super")

	// Submodule origin with two commits; the superproject will record the first.
	mustGit(t, "", "init", "--initial-branch=main", sub)
	mustGit(t, sub, "config", "user.email", "test@test.com")
	mustGit(t, sub, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(sub, "f"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, sub, "add", ".")
	mustGit(t, sub, "commit", "-m", "c1")
	c1, err := runGit(sub, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, sub, "commit", "-am", "c2")
	c2, err := runGit(sub, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	mustGit(t, sub, "checkout", c1) // record c1 in the super at add time

	// Superproject embedding the submodule at c1.
	mustGit(t, "", "init", "--initial-branch=main", super)
	mustGit(t, super, "config", "user.email", "test@test.com")
	mustGit(t, super, "config", "user.name", "Test")
	mustGit(t, super, "config", "protocol.file.allow", "always") // allow the local-path submodule
	mustGit(t, super, "submodule", "add", sub, "mod")
	mustGit(t, super, "add", ".")
	mustGit(t, super, "commit", "-m", "add submodule")

	// Simulate a returned pooled slot: the submodule working tree has drifted off
	// the superproject's recorded gitlink (a plain reset does not recurse into it).
	mustGit(t, filepath.Join(super, "mod"), "checkout", c2)
	if dirty, _ := IsDirty(super); !dirty {
		t.Fatal("fixture: a drifted submodule should make the worktree dirty")
	}

	if err := ResetWorktree(super, "main"); err != nil {
		t.Fatalf("ResetWorktree failed: %v", err)
	}

	if dirty, err := IsDirty(super); err != nil || dirty {
		t.Fatalf("worktree still dirty after reset; submodules were not re-synced (dirty=%v err=%v)", dirty, err)
	}
	if got, _ := runGit(filepath.Join(super, "mod"), "rev-parse", "HEAD"); got != c1 {
		t.Fatalf("submodule not re-aligned to recorded gitlink: got %s want %s", got, c1)
	}
}
