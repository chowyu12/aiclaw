package builtin

import (
	"context"
	"time"
)

func Handlers() map[string]func(context.Context, string) (string, error) {
	return map[string]func(context.Context, string) (string, error){
		"current_time": currentTime,
	}
}

func currentTime(_ context.Context, _ string) (string, error) {
	return time.Now().Format(time.RFC3339), nil
}
