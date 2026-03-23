package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// JSON wraps json.RawMessage with database Scanner/Valuer support for GORM.
type JSON json.RawMessage

func (j JSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	return string(j), nil
}

func (j *JSON) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}
	switch v := value.(type) {
	case []byte:
		*j = make(JSON, len(v))
		copy(*j, v)
	case string:
		*j = JSON(v)
	default:
		return fmt.Errorf("json: unsupported scan type %T", value)
	}
	return nil
}

func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return []byte(j), nil
}

func (j *JSON) UnmarshalJSON(data []byte) error {
	if data == nil || string(data) == "null" {
		*j = nil
		return nil
	}
	*j = make(JSON, len(data))
	copy(*j, data)
	return nil
}

func (JSON) GormDataType() string { return "text" }
