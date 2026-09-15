package computeruse

import (
	"fmt"
	"image/png"
	"os"
)

// pngDimensions reads a capture's pixel size. On a high-density display this
// differs from the click coordinate space, and reporting both is what stops a
// model from scaling coordinates off the image.
func pngDimensions(path string) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	config, err := png.DecodeConfig(file)
	if err != nil {
		return 0, 0, fmt.Errorf("decode %s: %w", path, err)
	}
	return config.Width, config.Height, nil
}
