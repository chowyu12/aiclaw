package agent

import (
	"context"
	"encoding/json"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"testing"
)

func TestPolicyUpdatePreservesAdmittedEnvironmentAndPersists(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	db := persistTestSession(t, s)
	previous := s.turnEnv("running", &recordingEmitter{})
	if err := s.ConfigureApproval(context.Background(), protocol.ApprovalAlways); err != nil {
		t.Fatal(err)
	}
	if previous.Policy != protocol.ApprovalBypass || s.turnEnv("queued", &recordingEmitter{}).Policy != protocol.ApprovalAlways {
		t.Fatal("policy leaked across turn boundary")
	}
	record, err := db.Load(context.Background(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	var config protocol.SessionStartParams
	_ = json.Unmarshal(record.Config, &config)
	if config.ApprovalPolicy != protocol.ApprovalAlways || s.applied.ApprovalPolicy != protocol.ApprovalAlways {
		t.Fatal("policy not checkpointed")
	}
	if err := s.ConfigureApproval(context.Background(), "invalid"); err == nil {
		t.Fatal("invalid policy accepted")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureApproval(context.Background(), protocol.ApprovalBypass); err == nil {
		t.Fatal("failed save acknowledged")
	}
	if s.turnEnv("later", &recordingEmitter{}).Policy != protocol.ApprovalAlways {
		t.Fatal("failed update changed policy")
	}
}
