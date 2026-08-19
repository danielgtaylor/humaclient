package humaclient

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// generateInto generates a client for api into a fresh temp dir, which it makes the
// working directory for the rest of the test, and returns the generated source. It
// also gates on the generated code compiling, since a generator regression that emits
// invalid Go is otherwise only visible to downstream users.
func generateInto(t *testing.T, api huma.API, pkgDir string) string {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "humaclient_test_*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tempDir) })

	oldDir, _ := os.Getwd()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(oldDir) })

	if err := GenerateClient(api); err != nil {
		t.Fatalf("GenerateClient: %v", err)
	}
	clientFile := pkgDir + "/client.go"
	src, err := os.ReadFile(clientFile)
	if err != nil {
		t.Fatalf("read generated client: %v", err)
	}
	// The generated client is standalone stdlib-only, so it builds on its own.
	if out, err := exec.Command("go", "build", clientFile).CombinedOutput(); err != nil {
		t.Fatalf("generated client does not compile: %v\n%s", err, out)
	}
	return string(src)
}

// runGeneratedProgram writes main.go alongside an already-generated client in the
// current directory, runs it against baseURL, and returns its stdout. The generated
// client is standalone stdlib-only, so the temp module needs no requirements.
func runGeneratedProgram(t *testing.T, source, baseURL string) string {
	t.Helper()
	if err := os.WriteFile("go.mod", []byte("module testprogram\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile("main.go", []byte(source), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	out, err := exec.Command("go", "run", ".", baseURL).CombinedOutput()
	if err != nil {
		t.Fatalf("running the generated client failed: %v\n%s", err, out)
	}
	return string(out)
}

// methodBody returns the source of the function whose declaration starts with prefix,
// so a test can assert on one generated method rather than the whole file.
func methodBody(t *testing.T, src, prefix string) string {
	t.Helper()
	start := strings.Index(src, prefix)
	if start < 0 {
		t.Fatalf("generated client has no method starting %q", prefix)
	}
	rest := src[start:]
	if end := strings.Index(rest, "\n}\n"); end >= 0 {
		return rest[:end+3]
	}
	return rest
}
