package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

func TestStartupFailureUsesSelectedJSONFormat(t *testing.T) {
	if os.Getenv("STORAGE_LOGGING_TEST_CHILD") == "1" {
		main()
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestStartupFailureUsesSelectedJSONFormat$")
	command.Env = append(os.Environ(), "STORAGE_LOGGING_TEST_CHILD=1", "LOG_FORMAT=json", "LOG_LEVEL=error", "STELLARMESH_STORAGE_ACCESS_FILE=")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err == nil || command.ProcessState.ExitCode() != 1 {
		t.Fatalf("expected startup failure: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &event); err != nil {
		t.Fatalf("startup output is not one JSON event: %v", err)
	}
	if event["level"] != "ERROR" || event["service"] != "storage-service" || stderr.Len() != 0 {
		t.Fatalf("unexpected startup log: %s; stderr=%s", stdout.String(), stderr.String())
	}
}
