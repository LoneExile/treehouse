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

// isolateGitConfig points git's global and system config at an empty file for
// the duration of the test, for both the fixture and the code under test. Any
// ambient protocol.file.allow=always would silently make the submodule tests
// below vacuous, so they must not inherit the developer's git config.
func isolateGitConfig(t *testing.T) {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_SYSTEM", empty)
}

// submoduleFixture builds a superproject with one submodule whose remote is a
// local path, plus a linked worktree standing in for a pooled slot. It returns
// the slot, the parent clone, the submodule's origin, and the submodule commit
// the superproject currently pins.
//
// The local-path remote is the whole point: a pool slot's submodule remote is
// always a path on disk, and a linked worktree gets its own submodule gitdir
// (<common>/worktrees/<name>/modules/<path>), so it must really clone and fetch
// over the "file" transport instead of reusing the parent clone's objects.
func submoduleFixture(t *testing.T) (slot, super, sub, pinned string) {
	t.Helper()
	isolateGitConfig(t)

	base := t.TempDir()
	sub = filepath.Join(base, "sub")
	super = filepath.Join(base, "super")
	slot = filepath.Join(base, "slot")

	mustGit(t, "", "init", "--initial-branch=main", sub)
	mustGit(t, sub, "config", "user.email", "test@test.com")
	mustGit(t, sub, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(sub, "f"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, sub, "add", ".")
	mustGit(t, sub, "commit", "-m", "c1")

	mustGit(t, "", "init", "--initial-branch=main", super)
	mustGit(t, super, "config", "user.email", "test@test.com")
	mustGit(t, super, "config", "user.name", "Test")
	// The fixture opts into the file transport explicitly; proving ResetWorktree
	// opts in on its own behalf is what the assertions below are for.
	mustGit(t, super, "-c", protocolFileAllow, "submodule", "add", sub, "mod")
	mustGit(t, super, "add", ".")
	mustGit(t, super, "commit", "-m", "add submodule")

	mustGit(t, super, "worktree", "add", "--detach", slot, "main")
	mustGit(t, slot, "-c", protocolFileAllow, "submodule", "update", "--init", "--recursive")

	pinned = submoduleHead(t, slot)
	return slot, super, sub, pinned
}

func submoduleHead(t *testing.T, slot string) string {
	t.Helper()
	head, err := runGit(filepath.Join(slot, "mod"), "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("reading submodule HEAD: %v", err)
	}
	return head
}

// TestResetWorktreeFetchesSubmoduleOverFileTransport is the regression test for
// the pool-starvation bug: the slot's gitlink pins a submodule commit that only
// the local-path remote has, and git refuses that fetch unless the submodule
// commands opt into the file transport. Before the fix the fetch failed, the
// gitlink stayed modified, and the slot read dirty forever.
func TestResetWorktreeFetchesSubmoduleOverFileTransport(t *testing.T) {
	slot, super, sub, _ := submoduleFixture(t)

	// Advance the gitlink in the PARENT clone, so the commit it now pins is
	// absent from the slot's own submodule object store.
	if err := os.WriteFile(filepath.Join(sub, "f"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, sub, "commit", "-am", "c2")
	ahead, err := runGit(sub, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	superMod := filepath.Join(super, "mod")
	mustGit(t, superMod, "-c", protocolFileAllow, "fetch", "origin")
	mustGit(t, superMod, "checkout", ahead)
	mustGit(t, super, "commit", "-am", "bump gitlink")

	// Put the slot on the bumped commit so its gitlink points at a submodule
	// commit it has to fetch, which is the state a reset has to recover from.
	mustGit(t, slot, "checkout", "--detach", "--force", "main")
	if dirty, _ := IsDirty(slot); !dirty {
		t.Fatal("fixture: an unfetchable gitlink should make the slot dirty")
	}

	// Non-vacuity guard: without opting into the file transport this is refused,
	// which is precisely what left the pool full of unusable slots.
	if _, err := runGit(slot, "submodule", "update", "--init", "--recursive", "--force"); err == nil {
		t.Fatal("fixture is vacuous: a plain submodule update should be refused over the file transport")
	} else if !strings.Contains(err.Error(), "transport 'file' not allowed") {
		t.Fatalf("fixture should fail on the file transport, got: %v", err)
	}

	if err := ResetWorktree(slot, "main"); err != nil {
		t.Fatalf("ResetWorktree failed: %v", err)
	}
	if dirty, err := IsDirty(slot); err != nil || dirty {
		t.Fatalf("slot still dirty after reset, so it would never be reissued (dirty=%v err=%v)", dirty, err)
	}
	if got := submoduleHead(t, slot); got != ahead {
		t.Fatalf("submodule not re-aligned to the recorded gitlink: got %s want %s", got, ahead)
	}
}

// TestResetWorktreeClearsDirtySubmoduleWorkingTree covers the other way a
// submodule keeps a slot permanently dirty: modified tracked files and leftover
// untracked files inside the submodule both surface as a modified gitlink.
func TestResetWorktreeClearsDirtySubmoduleWorkingTree(t *testing.T) {
	slot, _, _, pinned := submoduleFixture(t)
	mod := filepath.Join(slot, "mod")

	if err := os.WriteFile(filepath.Join(mod, "f"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mod, "junk"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "junk", "leftover"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty, _ := IsDirty(slot); !dirty {
		t.Fatal("fixture: a dirty submodule working tree should make the slot dirty")
	}

	if err := ResetWorktree(slot, "main"); err != nil {
		t.Fatalf("ResetWorktree failed: %v", err)
	}
	if dirty, err := IsDirty(slot); err != nil || dirty {
		t.Fatalf("slot still dirty after reset (dirty=%v err=%v)", dirty, err)
	}
	if got := submoduleHead(t, slot); got != pinned {
		t.Fatalf("submodule moved off the recorded gitlink: got %s want %s", got, pinned)
	}
	if _, err := os.Stat(filepath.Join(mod, "junk")); !os.IsNotExist(err) {
		t.Fatalf("untracked submodule content survived the reset (err=%v)", err)
	}
}

// TestResetWorktreeFailsWhenSubmodulesCannotBeAligned pins down the behaviour
// that hid this bug for so long: treating the submodule step as best-effort let
// a reset report success while leaving the slot dirty and unusable, so `return`
// printed "returned to pool" for a slot that was instantly dirty again. A slot
// whose submodules cannot be aligned must fail the reset loudly.
func TestResetWorktreeFailsWhenSubmodulesCannotBeAligned(t *testing.T) {
	slot, super, sub, _ := submoduleFixture(t)

	// Advance the gitlink so the slot needs a fetch to align...
	if err := os.WriteFile(filepath.Join(sub, "f"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, sub, "commit", "-am", "c2")
	superMod := filepath.Join(super, "mod")
	mustGit(t, superMod, "-c", protocolFileAllow, "fetch", "origin")
	mustGit(t, superMod, "checkout", "FETCH_HEAD")
	mustGit(t, super, "commit", "-am", "bump gitlink")

	// ...then make that fetch impossible. `submodule update` fetches through the
	// submodule's OWN remote, and that config lives in the slot's per-worktree
	// submodule gitdir, so it survives `reset --hard` and does not touch the
	// parent clone.
	gone := filepath.Join(t.TempDir(), "gone")
	mustGit(t, filepath.Join(slot, "mod"), "remote", "set-url", "origin", gone)
	mustGit(t, slot, "config", "submodule.mod.url", gone)

	err := ResetWorktree(slot, "main")
	if err == nil {
		t.Fatal("ResetWorktree reported success for a slot whose submodules could not be aligned")
	}
	dirty, dirtyErr := IsDirty(slot)
	if dirtyErr != nil {
		t.Fatal(dirtyErr)
	}
	// The whole point: the slot really is unusable, so the caller must hear about
	// it rather than being told the worktree was returned to the pool.
	if !dirty {
		t.Fatal("fixture: an unalignable gitlink should leave the slot dirty")
	}
	t.Logf("reset failed loudly on an unusable slot, as intended: %v", err)
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
