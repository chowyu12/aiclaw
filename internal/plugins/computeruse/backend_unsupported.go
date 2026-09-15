//go:build !darwin

package computeruse

import (
	"context"
	"image"
)

// unsupportedBackend stands in on hosts without an input synthesis backend.
// The bundled manifest declares `runtime.os: ["darwin"]`, so installing the
// plugin elsewhere already fails; this keeps the package building and turns a
// wrongly wired provider into a clear error instead of a panic.
type unsupportedBackend struct{}

func newBackend() Backend { return unsupportedBackend{} }

func (unsupportedBackend) Name() string { return "unsupported" }

func (unsupportedBackend) ScreenSize(context.Context) (image.Point, error) {
	return image.Point{}, ErrUnsupported
}

func (unsupportedBackend) Screenshot(context.Context, string) error { return ErrUnsupported }

func (unsupportedBackend) Click(context.Context, image.Point) error { return ErrUnsupported }

func (unsupportedBackend) TypeText(context.Context, string) error { return ErrUnsupported }

func (unsupportedBackend) Key(context.Context, []string, string) error { return ErrUnsupported }
