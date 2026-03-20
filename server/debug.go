package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const postDebugKeyPrefix = "post_debug_"

type postDebugPayload struct {
	Request  string `json:"request,omitempty"`
	Response string `json:"response,omitempty"`
}

func postDebugKey(postID string) string {
	return postDebugKeyPrefix + strings.TrimSpace(postID)
}

func (p *Plugin) savePostDebugPayload(postID, requestDebug, responseDebug string) error {
	postID = strings.TrimSpace(postID)
	if postID == "" {
		return nil
	}

	payload := postDebugPayload{
		Request:  strings.TrimSpace(requestDebug),
		Response: strings.TrimSpace(responseDebug),
	}
	if payload.Request == "" && payload.Response == "" {
		return nil
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode post debug payload: %w", err)
	}
	if appErr := p.API.KVSet(postDebugKey(postID), data); appErr != nil {
		return fmt.Errorf("failed to persist post debug payload: %w", appErr)
	}
	return nil
}

func (p *Plugin) getPostDebugPayload(postID string) (postDebugPayload, bool, error) {
	postID = strings.TrimSpace(postID)
	if postID == "" {
		return postDebugPayload{}, false, nil
	}

	data, appErr := p.API.KVGet(postDebugKey(postID))
	if appErr != nil {
		return postDebugPayload{}, false, fmt.Errorf("failed to load post debug payload: %w", appErr)
	}
	if len(data) == 0 {
		return postDebugPayload{}, false, nil
	}

	var payload postDebugPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return postDebugPayload{}, false, fmt.Errorf("failed to decode post debug payload: %w", err)
	}
	payload.Request = strings.TrimSpace(payload.Request)
	payload.Response = strings.TrimSpace(payload.Response)
	return payload, payload.Request != "" || payload.Response != "", nil
}

func stringPostProp(post any, key string) string {
	type propGetter interface {
		GetProp(string) any
	}

	getter, ok := post.(propGetter)
	if !ok {
		return ""
	}

	value := getter.GetProp(key)
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
