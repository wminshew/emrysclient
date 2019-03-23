package poll

import (
	"encoding/json"
)

// Response holds a longpolling response
// source: https://github.com/jcuga/golongpoll/blob/master/go-client/glpclient/client.go
type Response struct {
	Events    []Event `json:"events"`
	Timestamp int64   `json:"timestamp"`
}

// Event holds a longpolling event
// source: https://github.com/jcuga/golongpoll/blob/master/go-client/glpclient/client.go
type Event struct {
	// Timestamp is milliseconds since epoch to match javascripts Date.getTime()
	Timestamp int64  `json:"timestamp"`
	Category  string `json:"category"`
	// Data can be anything that is able to passed to json.Marshal()
	Data json.RawMessage `json:"data"`
}
