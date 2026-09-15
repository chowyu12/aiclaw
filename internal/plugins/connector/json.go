package connector

import (
	"encoding/json"

	"github.com/chowyu12/aiclaw/internal/model"
)

func unmarshal(payload model.JSON, target any) error {
	if len(payload) == 0 {
		return nil
	}
	return json.Unmarshal(payload, target)
}
