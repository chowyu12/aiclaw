package builtin

import (
	"context"
)

func Handlers() map[string]func(context.Context, string) (string, error) {
	return map[string]func(context.Context, string) (string, error){}
}
