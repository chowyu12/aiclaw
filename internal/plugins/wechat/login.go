package wechat

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/chowyu12/aiclaw/pkg/wechatlink"
)

// qrImagePixels is the rendered code's size. Large enough to scan from a
// phone held in front of the screen, small enough to embed inline.
const qrImagePixels = 320

// LoginQR is a scannable sign-in code.
//
// The relay returns two different strings and confusing them is easy: Token
// identifies the attempt when polling, while Payload is the text a phone must
// read out of the printed code. Payload is deliberately not named like a URL:
// it happens to look like one, but fetching it does nothing useful — it has to
// be rendered as a QR code and scanned.
type LoginQR struct {
	Token string
	// Payload is what the code encodes.
	Payload string
	// Image is Payload rendered as a PNG data URI, ready for an <img src>.
	Image string
}

// FetchLoginQR asks the relay for a sign-in code and renders it.
//
// Rendering happens locally: sending the payload to a QR image service would
// hand a live sign-in link for the user's WeChat account to a third party.
func FetchLoginQR(ctx context.Context) (*LoginQR, error) {
	result, err := wechatlink.FetchQRCode(ctx)
	if err != nil {
		return nil, err
	}
	return renderLoginQR(result.QRCode, result.QRCodeURL)
}

// renderLoginQR turns the relay's two strings into a scannable code. It is
// separate from the fetch so the rendering can be tested without contacting
// the relay.
func renderLoginQR(token, payload string) (*LoginQR, error) {
	token, payload = strings.TrimSpace(token), strings.TrimSpace(payload)
	if token == "" || payload == "" {
		return nil, fmt.Errorf("the relay returned an incomplete login code")
	}
	// Medium recovery keeps the code readable on screen without inflating it.
	png, err := qrcode.Encode(payload, qrcode.Medium, qrImagePixels)
	if err != nil {
		return nil, fmt.Errorf("render login QR code: %w", err)
	}
	return &LoginQR{
		Token:   token,
		Payload: payload,
		Image:   "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	}, nil
}
