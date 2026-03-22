package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

type BotRunRequest struct {
	BotID         string         `json:"bot_id"`
	UserID        string         `json:"user_id"`
	UserName      string         `json:"user_name"`
	ChannelID     string         `json:"channel_id"`
	RootID        string         `json:"root_id"`
	Prompt        string         `json:"prompt"`
	Inputs        map[string]any `json:"inputs"`
	FileIDs       []string       `json:"file_ids,omitempty"`
	Source        string         `json:"source"`
	TriggerPostID string         `json:"trigger_post_id"`
}

type BotRunResult struct {
	CorrelationID string `json:"correlation_id"`
	BotID         string `json:"bot_id"`
	BotUsername   string `json:"bot_username"`
	BotName       string `json:"bot_name"`
	Model         string `json:"model"`
	APIDurationMS int64  `json:"api_duration_ms,omitempty"`
	PostID        string `json:"post_id,omitempty"`
	Status        string `json:"status"`
	Output        string `json:"output,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
	ErrorDetail   string `json:"error_detail,omitempty"`
	ErrorHint     string `json:"error_hint,omitempty"`
	RequestURL    string `json:"request_url,omitempty"`
	HTTPStatus    int    `json:"http_status,omitempty"`
	Retryable     bool   `json:"retryable"`
}

type executionFailureView struct {
	HasFailure  bool
	StageLabel  string
	Message     string
	ErrorCode   string
	Detail      string
	Hint        string
	RequestURL  string
	HTTPStatus  int
	Retryable   bool
	InputDebug  string
	OutputDebug string
	APIDuration time.Duration
}

type successDebugView struct {
	Request string
	Output  string
}

const upstageBotPostType = "custom_upstage_bot"

func (p *Plugin) executeBotAndPost(ctx context.Context, request BotRunRequest) (*BotRunResult, error) {
	startedAt := time.Now()
	correlationID := uuid.NewString()

	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		return nil, err
	}

	channel, appErr := p.API.GetChannel(request.ChannelID)
	if appErr != nil {
		return nil, fmt.Errorf("failed to load channel: %w", appErr)
	}
	user, appErr := p.API.GetUser(request.UserID)
	if appErr != nil {
		return nil, fmt.Errorf("failed to load user: %w", appErr)
	}
	request.UserName = user.Username
	team := p.getTeamForChannel(channel)

	bot := cfg.getBotByID(request.BotID)
	if bot == nil {
		return nil, fmt.Errorf("unknown bot %q", request.BotID)
	}
	if !bot.isAllowedFor(user, channel, team) {
		return nil, fmt.Errorf("bot %q is not allowed in this context", bot.Username)
	}
	if !p.client.User.HasPermissionToChannel(request.UserID, request.ChannelID, model.PermissionReadChannel) {
		return nil, fmt.Errorf("user does not have access to the selected channel")
	}

	account, ok := p.getBotAccount(bot.ID)
	if !ok {
		if err := p.ensureBots(); err != nil {
			return nil, err
		}
		account, ok = p.getBotAccount(bot.ID)
		if !ok {
			return nil, fmt.Errorf("bot account %q is not available", bot.ID)
		}
	}

	prompt := strings.TrimSpace(request.Prompt)
	if len(prompt) > cfg.MaxInputLength {
		return nil, fmt.Errorf("message exceeds the maximum input length of %d characters", cfg.MaxInputLength)
	}

	attachments, err := p.collectBotAttachments(request.FileIDs, request.ChannelID)
	if err != nil {
		return nil, err
	}
	if len(attachments) == 0 {
		return nil, fmt.Errorf("attach at least one document file before asking @%s", bot.Username)
	}

	serviceConfig, err := cfg.serviceConfigForBot(*bot)
	if err != nil {
		return nil, err
	}

	results := make([]upstageDocumentResult, 0, len(attachments))
	requestDebugs := make([]upstageRequestDebug, 0, len(attachments))
	apiDurationTotal := time.Duration(0)
	for _, attachment := range attachments {
		result, httpStatus, apiDuration, invokeErr := p.invokeUpstageDocumentParse(ctx, serviceConfig, *bot, attachment, correlationID)
		apiDurationTotal += apiDuration
		if invokeErr != nil {
			failure := describeExecutionFailure(invokeErr, httpStatus >= 500 || httpStatus == 0, apiDurationTotal)
			record := newExecutionRecord(request, account.Definition, correlationID, "failed", prompt, failure.Message, failure.ErrorCode, failure.Retryable, startedAt, time.Now())
			p.appendExecutionHistory(request.UserID, record)
			p.logUsage(cfg, correlationID, request, account.Definition, "failed", failure.Message)
			if postErr := p.postFailure(channel, request.RootID, account, correlationID, failure); postErr != nil {
				p.API.LogError("Failed to post PII inference error response", "error", postErr, "correlation_id", correlationID)
			}
			return &BotRunResult{
				CorrelationID: correlationID,
				BotID:         account.Definition.ID,
				BotUsername:   account.Definition.Username,
				BotName:       account.Definition.DisplayName,
				Model:         account.Definition.Model,
				APIDurationMS: apiDurationTotal.Milliseconds(),
				Status:        "failed",
				ErrorMessage:  failure.Message,
				ErrorCode:     failure.ErrorCode,
				ErrorDetail:   failure.Detail,
				ErrorHint:     failure.Hint,
				RequestURL:    failure.RequestURL,
				HTTPStatus:    failure.HTTPStatus,
				Retryable:     failure.Retryable,
			}, invokeErr
		}
		results = append(results, result)
		requestDebugs = append(requestDebugs, result.RequestDebugs...)
	}

	// Debug: log result payload info for each attachment.
	for i, result := range results {
		resultLen := len(string(result.Response.Result))
		bodyLen := len(result.ResponseDebug.Body)
		payload := parsePIIResultPayload(result.Response.Result)
		entries := collectPIIFieldEntries(payload)
		schema := detectPIIResultSchema(payload)
		p.API.LogInfo("PII result debug",
			"index", i,
			"file", result.Attachment.Name,
			"mime", result.Attachment.MIMEType,
			"schema", schema,
			"result_len", resultLen,
			"body_len", bodyLen,
			"entries_from_result", len(entries),
			"result_nil", payload == nil,
			"correlation_id", correlationID,
		)
	}

	shouldMaskSensitive := bot.shouldMaskSensitiveData(cfg.MaskSensitiveData)
	documentContext := buildDocumentResponseMessage(prompt, results, cfg.MaxOutputLength)
	if shouldMaskSensitive {
		documentContext = truncateString(maskSensitiveContent(documentContext), cfg.MaxOutputLength)
	}

	output := documentContext
	debugView := successDebugView{
		Request: buildSuccessRequestDebugPayload(requestDebugs, ""),
		Output:  buildSuccessResponseDebugPayload(results),
	}
	if bot.hasVLLMPostProcess() {
		vllmConfig, vllmErr := cfg.serviceConfigForVLLMBot(*bot)
		if vllmErr != nil {
			output = buildVLLMFallbackOutput(documentContext, "vLLM 후처리 설정을 확인하지 못해 PII 추출 결과를 대신 표시합니다.")
		}
		if vllmErr == nil {
			vllmOutput, vllmDebug, invokeErr := p.invokeVLLMPostProcess(ctx, vllmConfig, prompt, documentContext, correlationID)
			if invokeErr != nil {
				failure := describeExecutionFailure(invokeErr, true, apiDurationTotal)
				output = buildVLLMFallbackOutput(documentContext, "vLLM 후처리에 실패해 PII 추출 결과를 대신 표시합니다.")
				debugView = successDebugView{
					Request: buildSuccessRequestDebugPayload(requestDebugs, failure.InputDebug),
					Output:  failure.OutputDebug,
				}
				p.API.LogWarn("vLLM post-processing failed; falling back to PII inference output", "correlation_id", correlationID, "bot_id", bot.ID, "error", failure.Message)
			} else {
				output = truncateString(vllmOutput, cfg.MaxOutputLength)
				debugView = successDebugView{
					Request: buildSuccessRequestDebugPayload(requestDebugs, vllmDebug),
					Output:  buildSuccessResponseDebugPayload(results),
				}
			}
		}
	}

	if shouldMaskSensitive {
		output = truncateString(maskSensitiveContent(output), cfg.MaxOutputLength)
	}

	var maskedFileIDs []string
	if len(bot.MaskPIIKeys) > 0 {
		var maskErr error
		maskedFileIDs, maskErr = p.maskAndUploadFiles(results, channel.Id, bot.MaskPIIKeys)
		if maskErr != nil {
			p.API.LogWarn("File masking failed", "error", maskErr.Error(), "correlation_id", correlationID)
		}
	}

	post, err := p.postSuccess(channel, request.RootID, account, correlationID, output, debugView, apiDurationTotal, maskedFileIDs)
	if err != nil {
		failure := describeExecutionFailure(err, true, apiDurationTotal)
		record := newExecutionRecord(request, account.Definition, correlationID, "failed", prompt, failure.Message, failure.ErrorCode, failure.Retryable, startedAt, time.Now())
		p.appendExecutionHistory(request.UserID, record)
		return nil, err
	}

	record := newExecutionRecord(request, account.Definition, correlationID, "completed", prompt, "", "", false, startedAt, time.Now())
	p.appendExecutionHistory(request.UserID, record)
	p.logUsage(cfg, correlationID, request, account.Definition, "completed", "")

	return &BotRunResult{
		CorrelationID: correlationID,
		BotID:         account.Definition.ID,
		BotUsername:   account.Definition.Username,
		BotName:       account.Definition.DisplayName,
		Model:         account.Definition.Model,
		APIDurationMS: apiDurationTotal.Milliseconds(),
		PostID:        post.Id,
		Status:        "completed",
		Output:        output,
	}, nil
}

func buildDocumentResponseMessage(_ string, results []upstageDocumentResult, maxLength int) string {
	sections := make([]string, 0, len(results))
	for _, result := range results {
		sections = append(sections, buildPIIResultSection(result))
	}

	return truncateString(strings.TrimSpace(strings.Join(sections, "\n\n")), maxLength)
}

type piiFieldEntry struct {
	Key           string
	Value         string
	Confidence    float64
	HasConfidence bool
	PageNumber    int
}

func buildPIIResultSection(result upstageDocumentResult) string {
	payload := parsePIIResultPayload(result.Response.Result)
	entries := collectPIIFieldEntries(payload)

	// Fallback: if no entries found from the result field, try the full response body.
	// Some API responses place PII fields outside the "result" key.
	if len(entries) == 0 && strings.TrimSpace(result.ResponseDebug.Body) != "" {
		fullPayload := parseDebugPayloadString(result.ResponseDebug.Body)
		if fullPayload != nil {
			entries = collectPIIFieldEntries(fullPayload)
			if payload == nil {
				payload = fullPayload
			}
		}
	}

	lines := []string{
		fmt.Sprintf("### %s", result.Attachment.Name),
		fmt.Sprintf("- Model: `%s`", defaultIfEmpty(strings.TrimSpace(result.Response.Model), defaultUpstageModel)),
	}
	if responseType := strings.TrimSpace(result.Response.Type); responseType != "" {
		lines = append(lines, fmt.Sprintf("- Type: `%s`", responseType))
	}
	if schema := detectPIIResultSchema(payload); schema != "" {
		lines = append(lines, fmt.Sprintf("- Schema: `%s`", schema))
	}
	if documentType := topLevelString(payload, "documentType"); documentType != "" {
		lines = append(lines, fmt.Sprintf("- Document Type: `%s`", documentType))
	}
	if confidence, ok := topLevelNumber(payload, "confidence"); ok {
		lines = append(lines, fmt.Sprintf("- Confidence: `%.1f%%`", confidence*100))
	}
	if result.Response.NumBilledPages > 0 {
		lines = append(lines, fmt.Sprintf("- Billed Pages: `%d`", result.Response.NumBilledPages))
	}

	if len(entries) == 0 {
		lines = append(lines, "", "_추출된 개인정보 필드가 없습니다. `PII API 응답 JSON 보기` 버튼에서 원본 JSON을 확인하세요._")
		return strings.Join(lines, "\n")
	}

	lines = append(lines, fmt.Sprintf("- Extracted Fields: `%d`", len(entries)))
	lines = append(lines, "", renderPIIFieldEntries(entries, 20))
	return strings.Join(lines, "\n")
}

func parsePIIResultPayload(raw json.RawMessage) any {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil
	}

	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	return payload
}

func parseDebugPayloadString(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return nil
	}
	return payload
}

func detectPIIResultSchema(payload any) string {
	root, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	if _, ok := root["fields"]; ok {
		return "oac"
	}
	for _, key := range []string{"document", "documents", "groups", "entities"} {
		if _, ok := root[key]; ok {
			return "ufp"
		}
	}
	return ""
}

func collectPIIFieldEntries(payload any) []piiFieldEntry {
	entries := make([]piiFieldEntry, 0, 16)
	seen := map[string]struct{}{}
	collectPIIFieldEntriesRecursive(payload, seen, &entries)
	return entries
}

func collectPIIFieldEntriesRecursive(value any, seen map[string]struct{}, entries *[]piiFieldEntry) {
	switch typed := value.(type) {
	case map[string]any:
		key := normalizePIIFieldKey(extractPIIFieldValue(typed["key"]))
		fieldType := strings.ToLower(strings.TrimSpace(stringValue(typed["type"])))
		fieldValue := extractPIIFieldValue(typed["refinedValue"])
		if fieldValue == "" {
			fieldValue = extractPIIFieldValue(typed["value"])
		}
		if key != "" && fieldValue != "" {
			signature := key + "\x00" + fieldValue
			if _, ok := seen[signature]; !ok {
				entry := piiFieldEntry{
					Key:   key,
					Value: fieldValue,
				}
				if confidence, ok := numberValue(typed["confidence"]); ok {
					entry.Confidence = confidence
					entry.HasConfidence = true
				} else if confidence, ok := numberValue(typed["entityConfidence"]); ok {
					entry.Confidence = confidence
					entry.HasConfidence = true
				}
				if pageNumber, ok := intValue(typed["pageNumber"]); ok {
					entry.PageNumber = pageNumber
				}
				seen[signature] = struct{}{}
				*entries = append(*entries, entry)
			}
		}

		if key != "" && fieldType == "group" {
			for _, nestedKey := range []string{"properties", "entities", "fields", "groups"} {
				if nestedValue, ok := typed[nestedKey]; ok {
					collectPIIFieldEntriesRecursive(nestedValue, seen, entries)
				}
			}
		}

		for _, nested := range typed {
			collectPIIFieldEntriesRecursive(nested, seen, entries)
		}
	case []any:
		for _, item := range typed {
			collectPIIFieldEntriesRecursive(item, seen, entries)
		}
	}
}

func renderPIIFieldEntries(entries []piiFieldEntry, limit int) string {
	displayed := len(entries)
	if limit > 0 && displayed > limit {
		displayed = limit
	}

	lines := make([]string, 0, displayed+1)
	for index, entry := range entries {
		if limit > 0 && index >= limit {
			lines = append(lines, fmt.Sprintf("- ... 외 `%d`개 필드", len(entries)-limit))
			break
		}

		line := fmt.Sprintf("- `%s`: `%s`", entry.Key, escapeInlineCode(formatPIIFieldValue(entry.Value)))
		if entry.HasConfidence {
			line += fmt.Sprintf(" _(%.1f%%)_", entry.Confidence*100)
		}
		if entry.PageNumber > 0 {
			line += fmt.Sprintf(" [p.%d]", entry.PageNumber)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func extractPIIFieldValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case json.Number:
		return strings.TrimSpace(typed.String())
	case float64, float32, int, int32, int64, bool:
		return strings.TrimSpace(fmt.Sprint(typed))
	case map[string]any:
		for _, key := range []string{"refinedValue", "value", "content", "text", "name"} {
			if nested := extractPIIFieldValue(typed[key]); nested != "" {
				return nested
			}
		}
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if nested := extractPIIFieldValue(item); nested != "" {
				parts = append(parts, nested)
			}
		}
		return strings.TrimSpace(strings.Join(parts, ", "))
	}
	return ""
}

func normalizePIIFieldKey(key string) string {
	key = strings.TrimSpace(key)
	for _, suffix := range []string{".content", ".value"} {
		if strings.HasSuffix(key, suffix) {
			return strings.TrimSpace(strings.TrimSuffix(key, suffix))
		}
	}
	return key
}

func formatPIIFieldValue(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "-"
	}
	return truncateString(value, 120)
}

func escapeInlineCode(value string) string {
	return strings.ReplaceAll(value, "`", "'")
}

func topLevelString(payload any, key string) string {
	root, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	return strings.TrimSpace(stringValue(root[key]))
}

func topLevelNumber(payload any, key string) (float64, bool) {
	root, ok := payload.(map[string]any)
	if !ok {
		return 0, false
	}
	return numberValue(root[key])
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil
	default:
		return 0, false
	}
}

func renderParsedContent(format, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if format == "html" {
		return convertUpstageHTMLFragmentToMessage("document", value)
	}
	if format == "json" {
		return "```json\n" + value + "\n```"
	}
	return value
}

func buildRenderableUpstageContent(response upstageParseResponse, preferred []string) (string, string) {
	if format, content := buildPageSeparatedContent(response, preferred); content != "" {
		return format, content
	}

	format, content := choosePreferredUpstageContent(response, preferred)
	if format == "html" {
		return "markdown", convertUpstageHTMLFragmentToMessage("document", content)
	}

	return format, content
}

func buildPageSeparatedContent(response upstageParseResponse, preferred []string) (string, string) {
	if response.Usage.Pages <= 0 || len(response.Elements) == 0 {
		return "", ""
	}

	elementsByPage := make(map[int][]upstageParseElement, response.Usage.Pages)
	maxPage := response.Usage.Pages
	for _, element := range response.Elements {
		if element.Page <= 0 {
			continue
		}
		elementsByPage[element.Page] = append(elementsByPage[element.Page], element)
		if element.Page > maxPage {
			maxPage = element.Page
		}
	}
	if len(elementsByPage) == 0 {
		return "", ""
	}

	sections := make([]string, 0, maxPage)
	chosenFormat := ""
	for page := 1; page <= maxPage; page++ {
		pageElements := elementsByPage[page]
		pageFormat, pageContent := choosePreferredUpstagePageContent(pageElements, preferred)
		if pageContent == "" {
			pageContent = "_이 페이지에는 추출된 본문이 없습니다._"
		}
		if pageFormat != "" && chosenFormat == "" {
			chosenFormat = pageFormat
		}

		renderFormat := defaultIfEmpty(pageFormat, chosenFormat)
		rendered := pageContent
		if renderFormat != "" {
			rendered = renderParsedContent(renderFormat, pageContent)
		}

		sections = append(sections, strings.TrimSpace(strings.Join([]string{
			fmt.Sprintf("#### Page %d", page),
			"",
			rendered,
		}, "\n")))
	}

	return defaultIfEmpty(chosenFormat, "markdown"), strings.Join(sections, "\n\n---\n\n")
}

func choosePreferredUpstagePageContent(elements []upstageParseElement, preferred []string) (string, string) {
	if len(elements) == 0 {
		return "", ""
	}

	for _, format := range preferred {
		if content := joinUpstagePageElementContent(elements, format); content != "" {
			return format, content
		}
	}

	for _, format := range []string{"markdown", "text", "html"} {
		if content := joinUpstagePageElementContent(elements, format); content != "" {
			if format == "html" {
				return "markdown", content
			}
			return format, content
		}
	}

	return "", ""
}

func joinUpstagePageElementContent(elements []upstageParseElement, format string) string {
	parts := make([]string, 0, len(elements))
	for _, element := range elements {
		value := renderableUpstageElementContent(element, format)
		if strings.TrimSpace(value) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(value))
	}

	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func (p *Plugin) ensureBots() error {
	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		p.setBotAccounts(map[string]botAccount{})
		p.setBotSyncState(botSyncState{
			LastError: err.Error(),
			UpdatedAt: time.Now().UnixMilli(),
			Entries:   []botSyncEntry{},
		})
		return err
	}
	if len(cfg.BotDefinitions) == 0 {
		p.setBotAccounts(map[string]botAccount{})
		deactivateErr := p.deactivateManagedBots(nil)
		lastError := ""
		if deactivateErr != nil {
			lastError = deactivateErr.Error()
		}
		p.setBotSyncState(botSyncState{
			LastError: lastError,
			UpdatedAt: time.Now().UnixMilli(),
			Entries:   []botSyncEntry{},
		})
		return nil
	}

	accounts := make(map[string]botAccount, len(cfg.BotDefinitions))
	syncEntries := make([]botSyncEntry, 0, len(cfg.BotDefinitions))
	configuredUsernames := make(map[string]struct{}, len(cfg.BotDefinitions))
	syncIssues := make([]string, 0)
	for _, definition := range cfg.BotDefinitions {
		configuredUsernames[definition.Username] = struct{}{}
		userID, statusMessage, ensureErr := p.ensureSingleBot(definition)
		entry := botSyncEntry{
			BotID:         definition.ID,
			Username:      definition.Username,
			DisplayName:   definition.DisplayName,
			Model:         definition.Model,
			UserID:        userID,
			Registered:    ensureErr == nil && userID != "",
			Active:        ensureErr == nil && userID != "",
			StatusMessage: statusMessage,
		}
		if ensureErr != nil {
			entry.StatusMessage = ensureErr.Error()
			entry.Active = false
			syncEntries = append(syncEntries, entry)
			syncIssues = append(syncIssues, ensureErr.Error())
			continue
		}
		accounts[definition.ID] = botAccount{
			Definition: definition,
			UserID:     userID,
		}
		syncEntries = append(syncEntries, entry)
	}

	if deactivateErr := p.deactivateManagedBots(configuredUsernames); deactivateErr != nil {
		syncIssues = append(syncIssues, deactivateErr.Error())
	}

	p.setBotAccounts(accounts)
	p.setBotSyncState(botSyncState{
		LastError: joinSyncIssues(syncIssues),
		UpdatedAt: time.Now().UnixMilli(),
		Entries:   syncEntries,
	})
	return nil
}

func (p *Plugin) ensureSingleBot(definition BotDefinition) (string, string, error) {
	description := botDescription(definition)
	displayName := definition.DisplayName

	existingUser, appErr := p.API.GetUserByUsername(definition.Username)
	if appErr == nil && existingUser != nil {
		if !existingUser.IsBot {
			return "", "", fmt.Errorf("username @%s is already used by a regular Mattermost account", definition.Username)
		}

		statusMessage := ""
		if _, err := p.client.Bot.Get(existingUser.Id, true); err == nil {
			if _, err := p.client.Bot.Patch(existingUser.Id, &model.BotPatch{
				DisplayName: &displayName,
				Description: &description,
			}); err != nil && !isBotNotFoundError(err) {
				return "", "", fmt.Errorf("failed to update Upstage bot @%s: %w", definition.Username, err)
			}
			if _, err := p.client.Bot.UpdateActive(existingUser.Id, true); err != nil && !isBotNotFoundError(err) {
				return "", "", fmt.Errorf("failed to activate Upstage bot @%s: %w", definition.Username, err)
			}
			p.API.LogInfo("Ensured PII inference bot", "bot_username", definition.Username, "model", definition.Model, "action", "linked_existing")
			return existingUser.Id, statusMessage, nil
		}

		statusMessage = "기존 봇 사용자 계정을 연결했습니다. Bot 메타데이터 조회는 실패했지만 메시지 전송은 계속 시도합니다."
		p.API.LogWarn("Linked PII bot user without bot metadata", "bot_username", definition.Username, "user_id", existingUser.Id)
		return existingUser.Id, statusMessage, nil
	}

	if appErr != nil && appErr.StatusCode != http.StatusNotFound {
		return "", "", fmt.Errorf("failed to look up Mattermost user @%s: %w", definition.Username, appErr)
	}

	newBot := &model.Bot{
		Username:    definition.Username,
		DisplayName: definition.DisplayName,
		Description: description,
	}
	if err := p.client.Bot.Create(newBot); err != nil {
		existingUser, existingErr := p.API.GetUserByUsername(definition.Username)
		if existingErr == nil && existingUser != nil && existingUser.IsBot {
			p.API.LogWarn("Recovered PII bot by linking an already existing bot user", "bot_username", definition.Username, "user_id", existingUser.Id, "error", err.Error())
			return existingUser.Id, "이미 존재하는 봇 사용자 계정에 연결했습니다.", nil
		}
		return "", "", fmt.Errorf("failed to create Upstage bot @%s: %w", definition.Username, err)
	}

	p.API.LogInfo("Ensured PII inference bot", "bot_username", definition.Username, "model", definition.Model, "action", "created")
	return newBot.UserId, "", nil
}

func (p *Plugin) deactivateManagedBots(configuredUsernames map[string]struct{}) error {
	bots, err := p.client.Bot.List(0, 200, pluginapi.BotOwner(manifest.Id))
	if err != nil {
		return fmt.Errorf("failed to list plugin bots for deactivation: %w", err)
	}

	issues := make([]string, 0)
	for _, bot := range bots {
		if bot == nil {
			continue
		}
		if _, keep := configuredUsernames[strings.ToLower(bot.Username)]; keep {
			continue
		}
		if _, err := p.client.Bot.UpdateActive(bot.UserId, false); err != nil {
			if isBotNotFoundError(err) {
				p.API.LogWarn("Skipped deactivation for missing PII bot metadata", "bot_username", bot.Username, "user_id", bot.UserId, "error", err.Error())
				continue
			}
			issues = append(issues, fmt.Sprintf("failed to deactivate removed PII bot @%s: %s", bot.Username, err.Error()))
			continue
		}
		p.API.LogInfo("Deactivated removed PII bot", "bot_username", bot.Username, "user_id", bot.UserId)
	}

	if len(issues) > 0 {
		return fmt.Errorf("%s", strings.Join(issues, "; "))
	}
	return nil
}

func (p *Plugin) ensureBotInChannel(channelID, botUserID string) error {
	if channelID == "" || botUserID == "" {
		return nil
	}
	if _, appErr := p.API.GetChannelMember(channelID, botUserID); appErr == nil {
		return nil
	}
	if _, appErr := p.API.AddUserToChannel(channelID, botUserID, ""); appErr != nil {
		return fmt.Errorf("failed to add bot to channel: %w", appErr)
	}
	return nil
}

func (p *Plugin) postSuccess(channel *model.Channel, rootID string, account botAccount, correlationID, output string, debugView successDebugView, apiDuration time.Duration, fileIDs []string) (*model.Post, error) {
	if err := p.ensureBotInChannel(channel.Id, account.UserID); err != nil {
		return nil, err
	}

	hasRequestDebug := strings.TrimSpace(debugView.Request) != ""
	hasResponseDebug := strings.TrimSpace(debugView.Output) != ""
	props := map[string]any{
		"from_bot":                "true",
		"upstage_bot_id":          account.Definition.ID,
		"upstage_correlation_id":  correlationID,
		"upstage_api_duration_ms": apiDuration.Milliseconds(),
		"upstage_has_request_debug":  hasRequestDebug,
		"upstage_has_response_debug": hasResponseDebug,
		"upstage_model":           account.Definition.Model,
		"upstage_document_parser": "true",
	}
	if strings.TrimSpace(debugView.Request) != "" {
		props["upstage_request_input"] = debugView.Request
	}
	if strings.TrimSpace(debugView.Output) != "" && len(debugView.Output) < 256*1024 {
		props["upstage_response_output"] = debugView.Output
	}

	post, appErr := p.API.CreatePost(&model.Post{
		UserId:    account.UserID,
		ChannelId: channel.Id,
		RootId:    rootID,
		Type:      upstageBotPostType,
		Message:   buildBotResponseMessage(output, correlationID, apiDuration),
		Props:     props,
		FileIds:   fileIDs,
	})
	if appErr != nil {
		return nil, fmt.Errorf("failed to create Upstage response post: %w", appErr)
	}
	if hasRequestDebug || hasResponseDebug {
		if err := p.savePostDebugPayload(post.Id, debugView.Request, debugView.Output); err != nil {
			p.API.LogWarn("Failed to persist PII success debug payload", "post_id", post.Id, "correlation_id", correlationID, "error", err.Error())
		}
	}
	return post, nil
}

func buildVLLMFallbackOutput(documentContext, notice string) string {
	parts := []string{
		"_" + strings.TrimSpace(notice) + "_",
		"",
		strings.TrimSpace(documentContext),
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func (p *Plugin) postFailure(channel *model.Channel, rootID string, account botAccount, correlationID string, failure executionFailureView) error {
	if err := p.ensureBotInChannel(channel.Id, account.UserID); err != nil {
		return err
	}

	hasRequestDebug := strings.TrimSpace(failure.InputDebug) != ""
	hasResponseDebug := strings.TrimSpace(failure.OutputDebug) != ""

	post, appErr := p.API.CreatePost(&model.Post{
		UserId:    account.UserID,
		ChannelId: channel.Id,
		RootId:    rootID,
		Type:      upstageBotPostType,
		Message:   buildBotFailureMessage(account.Definition, correlationID, failure),
		Props: map[string]any{
			"from_bot":                "true",
			"upstage_bot_id":          account.Definition.ID,
			"upstage_correlation_id":  correlationID,
			"upstage_api_duration_ms": failure.APIDuration.Milliseconds(),
			"upstage_model":           account.Definition.Model,
			"upstage_has_request_debug":  hasRequestDebug,
			"upstage_has_response_debug": hasResponseDebug,
			"upstage_error":           "true",
			"upstage_error_code":      failure.ErrorCode,
			"upstage_error_input":     failure.InputDebug,
			"upstage_document_parser": "true",
		},
	})
	if appErr != nil {
		return fmt.Errorf("failed to create Upstage error post: %w", appErr)
	}
	if hasRequestDebug || hasResponseDebug {
		if err := p.savePostDebugPayload(post.Id, failure.InputDebug, failure.OutputDebug); err != nil {
			p.API.LogWarn("Failed to persist PII failure debug payload", "post_id", post.Id, "correlation_id", correlationID, "error", err.Error())
		}
	}
	return nil
}

func (p *Plugin) postInstruction(channel *model.Channel, rootID string, account botAccount, message string) error {
	if channel == nil || strings.TrimSpace(message) == "" {
		return nil
	}
	if err := p.ensureBotInChannel(channel.Id, account.UserID); err != nil {
		return err
	}

	_, appErr := p.API.CreatePost(&model.Post{
		UserId:    account.UserID,
		ChannelId: channel.Id,
		RootId:    rootID,
		Type:      upstageBotPostType,
		Message:   strings.TrimSpace(message),
		Props: map[string]any{
			"from_bot":                "true",
			"upstage_bot_id":          account.Definition.ID,
			"upstage_document_parser": "true",
		},
	})
	if appErr != nil {
		return fmt.Errorf("failed to create Upstage instruction post: %w", appErr)
	}
	return nil
}

func responseRootID(post *model.Post) string {
	if post == nil {
		return ""
	}
	if post.RootId != "" {
		return post.RootId
	}
	return post.Id
}

func (p *Plugin) logUsage(cfg *runtimeConfiguration, correlationID string, request BotRunRequest, bot BotDefinition, status, errorMessage string) {
	if !cfg.EnableUsageLogs {
		return
	}
	p.API.LogInfo("PII inference execution", "correlation_id", correlationID, "bot_id", bot.ID, "bot_username", bot.Username, "model", bot.Model, "user_id", request.UserID, "channel_id", request.ChannelID, "source", request.Source, "status", status, "error", errorMessage, "attachment_count", len(request.FileIDs))
}

func botDescription(bot BotDefinition) string {
	description := strings.TrimSpace(bot.Description)
	if description != "" {
		return description
	}
	return fmt.Sprintf("PII inference bot using %s", bot.Model)
}

func buildBotResponseMessage(output, correlationID string, apiDuration time.Duration) string {
	body := strings.TrimSpace(output)
	if body == "" {
		body = "_빈 응답이 반환되었습니다._"
	}

	lines := []string{
		body,
		"",
		fmt.Sprintf("_Correlation ID:_ `%s`", correlationID),
	}
	if apiDuration > 0 {
		lines = append(lines, fmt.Sprintf("_PII API 응답 시간:_ `%s`", formatUpstageAPIDuration(apiDuration)))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func describeExecutionFailure(err error, defaultRetryable bool, apiDuration time.Duration) executionFailureView {
	if err == nil {
		return executionFailureView{}
	}

	var callErr *upstageCallError
	if errors.As(err, &callErr) {
		return executionFailureView{
			HasFailure:  true,
			StageLabel:  "PII Inference API",
			Message:     callErr.Error(),
			ErrorCode:   callErr.Code,
			Detail:      callErr.Detail,
			Hint:        callErr.Hint,
			RequestURL:  callErr.RequestURL,
			HTTPStatus:  callErr.StatusCode,
			Retryable:   callErr.Retryable,
			InputDebug:  callErr.InputDebug,
			OutputDebug: callErr.OutputDebug,
			APIDuration: apiDuration,
		}
	}

	var vllmErr *vllmCallError
	if errors.As(err, &vllmErr) {
		return executionFailureView{
			HasFailure:  true,
			StageLabel:  "vLLM 후처리",
			Message:     vllmErr.Error(),
			ErrorCode:   vllmErr.Code,
			Detail:      vllmErr.Detail,
			Hint:        vllmErr.Hint,
			RequestURL:  vllmErr.RequestURL,
			HTTPStatus:  vllmErr.StatusCode,
			Retryable:   vllmErr.Retryable,
			InputDebug:  vllmErr.InputDebug,
			OutputDebug: vllmErr.OutputDebug,
			APIDuration: apiDuration,
		}
	}

	return executionFailureView{
		HasFailure:  true,
		Message:     strings.TrimSpace(err.Error()),
		Retryable:   defaultRetryable,
		APIDuration: apiDuration,
	}
}

func buildBotFailureMessage(bot BotDefinition, correlationID string, failure executionFailureView) string {
	modelLabel := bot.Model
	if strings.Contains(failure.StageLabel, "vLLM") && strings.TrimSpace(bot.VLLMModel) != "" {
		modelLabel = bot.VLLMModel
	}
	lines := []string{
		fmt.Sprintf("%s 호출에 실패했습니다. 모델: `%s`", defaultIfEmpty(strings.TrimSpace(failure.StageLabel), "PII bot 실행"), modelLabel),
	}

	if failure.Message != "" {
		lines = append(lines, "", failure.Message)
	}
	if failure.Detail != "" && !strings.Contains(failure.Message, "상세: "+failure.Detail) {
		lines = append(lines, "", "상세: "+failure.Detail)
	}
	if failure.Hint != "" && !strings.Contains(failure.Message, "조치: "+failure.Hint) {
		lines = append(lines, "", "조치: "+failure.Hint)
	}
	if failure.HTTPStatus > 0 && !strings.Contains(failure.Message, "HTTP 상태:") {
		lines = append(lines, "", fmt.Sprintf("HTTP 상태: `%d`", failure.HTTPStatus))
	}
	if failure.Retryable {
		lines = append(lines, "", "_재시도 가능:_ 예")
	}
	if failure.InputDebug != "" || failure.OutputDebug != "" {
		lines = append(lines, "", "_상단의 요청/응답 파라미터 버튼에서 원본 payload를 확인할 수 있습니다._")
	}
	lines = append(lines, "", fmt.Sprintf("_Correlation ID:_ `%s`", correlationID))
	if failure.APIDuration > 0 {
		lines = append(lines, fmt.Sprintf("_PII API 응답 시간:_ `%s`", formatUpstageAPIDuration(failure.APIDuration)))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func buildSuccessRequestDebugPayload(upstageRequestDebugs []upstageRequestDebug, vllmRequestDebug string) string {
	payload := map[string]any{}
	if upstageDebug := marshalUpstageRequestDebugs(upstageRequestDebugs); upstageDebug != "" {
		var parsed any
		if err := json.Unmarshal([]byte(upstageDebug), &parsed); err == nil {
			payload["pii_inference"] = parsed
		}
	}
	if strings.TrimSpace(vllmRequestDebug) != "" {
		var parsed any
		if err := json.Unmarshal([]byte(vllmRequestDebug), &parsed); err == nil {
			payload["vllm"] = parsed
		}
	}
	if len(payload) == 0 {
		return ""
	}
	return marshalDebugPayload(payload)
}

func buildSuccessResponseDebugPayload(results []upstageDocumentResult) string {
	if len(results) == 0 {
		return ""
	}

	items := make([]map[string]any, 0, len(results))
	for _, result := range results {
		item := map[string]any{
			"filename": result.Attachment.Name,
		}
		if result.ResponseDebug.StatusCode > 0 {
			item["status_code"] = result.ResponseDebug.StatusCode
		}
		if requestID := strings.TrimSpace(result.ResponseDebug.RequestID); requestID != "" {
			item["request_id"] = requestID
		}
		if payload := parseDebugPayloadString(result.ResponseDebug.Body); payload != nil {
			item["response"] = payload
		} else if body := strings.TrimSpace(result.ResponseDebug.Body); body != "" {
			item["response_body"] = body
		} else if payload := buildSuccessResponseDebugFallback(result.Response); payload != nil {
			item["response"] = payload
		}
		items = append(items, item)
	}

	return marshalDebugPayload(map[string]any{"pii_inference": items})
}

func buildSuccessResponseDebugFallback(response upstageParseResponse) any {
	payload := map[string]any{}
	if responseType := strings.TrimSpace(response.Type); responseType != "" {
		payload["type"] = responseType
	}
	if response.NumBilledPages > 0 {
		payload["numBilledPages"] = response.NumBilledPages
	}
	if modelName := strings.TrimSpace(response.Model); modelName != "" {
		payload["model"] = modelName
	}
	if resultPayload := parsePIIResultPayload(response.Result); resultPayload != nil {
		payload["result"] = resultPayload
	}
	if len(payload) == 0 {
		return nil
	}
	return payload
}

func formatUpstageAPIDuration(duration time.Duration) string {
	if duration <= 0 {
		return "0.00초"
	}

	seconds := duration.Seconds()
	switch {
	case seconds < 10:
		return fmt.Sprintf("%.2f초", seconds)
	case seconds < 100:
		return fmt.Sprintf("%.1f초", seconds)
	default:
		return fmt.Sprintf("%.0f초", seconds)
	}
}

func isBotNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "resource bot not found") ||
		strings.Contains(lower, "bot does not exist") ||
		strings.Contains(lower, "unable to get bot")
}

func joinSyncIssues(issues []string) string {
	filtered := make([]string, 0, len(issues))
	for _, issue := range issues {
		issue = strings.TrimSpace(issue)
		if issue == "" {
			continue
		}
		filtered = append(filtered, issue)
	}
	return strings.Join(filtered, " | ")
}
