package wechat

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
	"testing"
)

// The relay returns a polling token and the text the printed code encodes.
// The payload looks like a URL but is not an image address: binding it to an
// <img src> is what produced a broken image in the settings dialog, so what
// the UI receives has to be the rendered PNG.
func TestRenderLoginQRProducesADecodablePNG(t *testing.T) {
	const payload = "https://liteapp.weixin.qq.com/q/7GiQu1?qrcode=f4e1c0e64a0028837f8a3a735d458494&bot_type=3"
	qr, err := renderLoginQR("f4e1c0e64a0028837f8a3a735d458494", payload)
	if err != nil {
		t.Fatal(err)
	}
	if qr.Token != "f4e1c0e64a0028837f8a3a735d458494" {
		t.Fatalf("token = %q", qr.Token)
	}
	if qr.Payload != payload {
		t.Fatalf("payload = %q", qr.Payload)
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(qr.Image, prefix) {
		t.Fatalf("image is not a PNG data URI: %.40q", qr.Image)
	}
	// The image must never be the payload itself.
	if strings.Contains(qr.Image, "liteapp.weixin.qq.com") {
		t.Fatal("the payload leaked into the image field instead of being rendered")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(qr.Image, prefix))
	if err != nil {
		t.Fatalf("image is not valid base64: %v", err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("image is not a valid PNG: %v", err)
	}
	if config.Width != qrImagePixels || config.Height != qrImagePixels {
		t.Fatalf("image = %dx%d, want %dx%d", config.Width, config.Height, qrImagePixels, qrImagePixels)
	}
}

// A half-filled response must fail rather than render a code that cannot be
// polled or scanned.
func TestRenderLoginQRRejectsIncompleteResponses(t *testing.T) {
	for name, values := range map[string][2]string{
		"no token":   {"", "https://liteapp.weixin.qq.com/q/x"},
		"no payload": {"token", ""},
		"blank":      {"   ", "   "},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := renderLoginQR(values[0], values[1]); err == nil {
				t.Fatal("an incomplete login code was accepted")
			}
		})
	}
}
