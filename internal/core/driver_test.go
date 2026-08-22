package core

import (
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestSamplingRequestKeepsProviderOutOfThreadIdentity(t *testing.T) {
	request := SamplingRequest{Thread: modelThreadForTest(), Messages: []SamplingMessage{{Role: "user", Content: "hello"}}}
	if request.Thread.ProviderID == 0 || len(request.Messages) != 1 {
		t.Fatalf("invalid sampling request: %#v", request)
	}
}

func modelThreadForTest() model.Thread {
	return model.Thread{ProviderID: 1, ModelName: "configured-model"}
}
