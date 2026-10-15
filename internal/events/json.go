package events

import "encoding/json"

func marshalEvent(event Event) ([]byte, error) {
	return json.Marshal(event)
}
