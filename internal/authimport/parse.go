// Package authimport 将 Grok CLI / CPA xAI 凭证 JSON 解析为 catalog 账号。
// 逻辑改编自 /opt/grokbuild-proxy/internal/auth/import_grok.go（仅导入）。
package authimport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
)

// ErrImportEntryLimit 表示有界解析器观察到的凭证条目超过调用方允许上限。
var ErrImportEntryLimit = errors.New("authimport: entry limit exceeded")

const (
	// Issuer 为生产 xAI OIDC issuer。
	Issuer = "https://auth.x.ai"
	// DefaultClientID 为公开 Grok CLI OAuth client_id。
	DefaultClientID = "b1a00492-073a-47ea-816f-4c329264a828"

	maxImportWarnings = 256
)

// GrokAuthEntry 为 ~/.grok/auth.json 或 CPA 导出中的一条凭证。
type GrokAuthEntry struct {
	Type          string `json:"type,omitempty"`
	Key           string `json:"key"`
	AccessToken   string `json:"access_token,omitempty"`
	AuthMode      string `json:"auth_mode,omitempty"`
	CreateTime    string `json:"create_time,omitempty"`
	UserID        string `json:"user_id,omitempty"`
	Email         string `json:"email,omitempty"`
	FirstName     string `json:"first_name,omitempty"`
	ProfileImage  string `json:"profile_image_asset_id,omitempty"`
	PrincipalType string `json:"principal_type,omitempty"`
	PrincipalID   string `json:"principal_id,omitempty"`
	TeamID        string `json:"team_id,omitempty"`
	CodingOptOut  bool   `json:"coding_data_retention_opt_out,omitempty"`
	RefreshToken  string `json:"refresh_token"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	Expired       string `json:"expired,omitempty"`
	OIDCIssuer    string `json:"oidc_issuer,omitempty"`
	OIDCClientID  string `json:"oidc_client_id,omitempty"`
	Sub           string `json:"sub,omitempty"`
	Disabled      bool   `json:"disabled,omitempty"`
	ProxyURL      string `json:"proxy_url,omitempty"`
	Proxy         string `json:"proxy,omitempty"`
}

// ImportedCredential 为从 auth JSON 产出的规范化凭证。
type ImportedCredential struct {
	SourceKey    string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Email        string
	UserID       string
	TeamID       string
	OIDCIssuer   string
	OIDCClientID string
	AuthMode     string
	Disabled     bool
	ProxyURL     string
	Warnings     []ImportWarning
	Raw          GrokAuthEntry
}

// ImportWarning 描述已识别但未消费的导入字段。
// 有意不包含字段值，避免诊断泄露凭证。
type ImportWarning struct {
	Source  string `json:"source,omitempty"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ParseGrokAuthJSON 解析 Grok CLI / CPA auth 文档（单对象、map、数组、包装）。
func ParseGrokAuthJSON(data []byte) ([]ImportedCredential, error) {
	credentials, _, err := ParseGrokAuthJSONDetailed(data)
	return credentials, err
}

// ParseGrokAuthJSONDetailed 同时报告字段级警告。
func ParseGrokAuthJSONDetailed(data []byte) ([]ImportedCredential, []ImportWarning, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil, fmt.Errorf("authimport: empty document")
	}
	// 多文档：存在多个顶层 JSON 值时合并。
	if multi, ok := tryMultiDoc(data); ok {
		return multi, nil, nil
	}
	switch data[0] {
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil {
			return nil, nil, fmt.Errorf("authimport: parse array: %w", err)
		}
		credentials, err := normalizeRawEntries(values, "entry")
		return credentials, nil, err
	case '{':
		bare, bareWarnings, err := decodeEntry(data, "default")
		if err != nil {
			return nil, nil, fmt.Errorf("authimport: parse: %w", err)
		}
		if entryHasToken(bare) {
			if err := validateCPAType("default", bare); err != nil {
				return nil, nil, err
			}
			credential, err := normalizeEntry("default", bare)
			if err != nil {
				return nil, nil, err
			}
			credential.Warnings = bareWarnings
			return []ImportedCredential{credential}, nil, nil
		}

		members, err := decodeObjectMembers(data)
		if err != nil {
			return nil, nil, fmt.Errorf("authimport: parse object: %w", err)
		}
		out := make([]ImportedCredential, 0, len(members))
		var warnings []ImportWarning
		occurrences := make(map[string]int)
		for _, member := range members {
			occurrences[member.Name]++
			source := member.Name
			if occurrences[member.Name] > 1 {
				source = fmt.Sprintf("%s#entry%d", member.Name, occurrences[member.Name])
			}
			if member.Name == "accounts" || member.Name == "credentials" {
				var values []json.RawMessage
				if err := json.Unmarshal(member.Value, &values); err != nil {
					return nil, nil, fmt.Errorf("authimport: field %q must be an array", member.Name)
				}
				nested, err := normalizeRawEntries(values, source)
				if err != nil {
					return nil, nil, err
				}
				out = append(out, nested...)
				continue
			}
			if firstNonSpace(member.Value) != '{' {
				warnings = append(warnings, unsupportedFieldWarning("document", member.Name))
				continue
			}
			entry, entryWarnings, err := decodeEntry(member.Value, source)
			if err != nil {
				return nil, nil, fmt.Errorf("authimport: field %q: %w", member.Name, err)
			}
			if !entryHasToken(entry) {
				warnings = append(warnings, unsupportedFieldWarning("document", member.Name))
				continue
			}
			if err := validateCPAType(source, entry); err != nil {
				return nil, nil, err
			}
			credential, err := normalizeEntry(source, entry)
			if err != nil {
				return nil, nil, err
			}
			credential.Warnings = entryWarnings
			out = append(out, credential)
		}
		if len(out) == 0 {
			if len(warnings) > 0 {
				fields := make([]string, 0, len(warnings))
				for _, warning := range warnings {
					fields = append(fields, warning.Field)
				}
				return nil, warnings, fmt.Errorf("authimport: no credential entries found; unsupported fields: %s", strings.Join(fields, ", "))
			}
			return nil, nil, fmt.Errorf("authimport: no credential entries found")
		}
		return out, warnings, nil
	default:
		return nil, nil, fmt.Errorf("authimport: expected JSON object or array")
	}
}

// ParseGrokAuthJSONDetailedLimit 在完整解析前做条目数预检。
// maxEntries 非正时保持无界行为。
func ParseGrokAuthJSONDetailedLimit(data []byte, maxEntries int) ([]ImportedCredential, []ImportWarning, error) {
	if maxEntries > 0 {
		count, err := countGrokAuthEntries(data, maxEntries)
		if err != nil {
			return nil, nil, err
		}
		if count > maxEntries {
			return nil, nil, fmt.Errorf("%w (max %d)", ErrImportEntryLimit, maxEntries)
		}
	}
	return ParseGrokAuthJSONDetailed(data)
}

// tryMultiDoc 检测拼接的多个顶层 JSON 值（多文档 JSON）。
// 单文档时返回 ok=false。
func tryMultiDoc(data []byte) ([]ImportedCredential, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var docs []json.RawMessage
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, false
		}
		docs = append(docs, append(json.RawMessage(nil), raw...))
		if len(docs) > 1 {
			// 继续排空；随后逐文档解析。
			continue
		}
	}
	if len(docs) <= 1 {
		return nil, false
	}
	var out []ImportedCredential
	for i, doc := range docs {
		creds, _, err := ParseGrokAuthJSONDetailed(doc)
		if err != nil {
			// 多文档仅在每个文档独立有效时成功。
			return nil, false
		}
		for j := range creds {
			if creds[j].SourceKey == "default" {
				creds[j].SourceKey = fmt.Sprintf("doc%d", i)
			} else {
				creds[j].SourceKey = fmt.Sprintf("doc%d:%s", i, creds[j].SourceKey)
			}
		}
		out = append(out, creds...)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func countGrokAuthEntries(data []byte, limit int) (int, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return 0, fmt.Errorf("authimport: empty document")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	total := 0
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return 0, err
		}
		remaining := 0
		if limit > 0 {
			remaining = limit - total
			if remaining <= 0 {
				return limit + 1, nil
			}
		}
		count, err := countSingleGrokAuthDocument(raw, remaining)
		if err != nil {
			return 0, err
		}
		total += count
		if limit > 0 && total > limit {
			return limit + 1, nil
		}
	}
	return total, nil
}

func countSingleGrokAuthDocument(data []byte, limit int) (int, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return 0, fmt.Errorf("authimport: empty document")
	}
	if data[0] == '[' {
		return countTopLevelArray(data, limit)
	}
	if data[0] != '{' {
		return 0, fmt.Errorf("authimport: expected JSON object or array")
	}
	bare, err := topLevelBareHasToken(data)
	if err != nil {
		return 0, fmt.Errorf("authimport: parse object: %w", err)
	}
	if bare {
		return 1, nil
	}
	return countCredentialMap(data, limit)
}

func countTopLevelArray(data []byte, limit int) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := expectJSONDelimiter(decoder, '['); err != nil {
		return 0, err
	}
	count := 0
	for decoder.More() {
		if limit > 0 && count >= limit {
			return limit + 1, nil
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return 0, err
		}
		count++
	}
	if err := finishJSONContainer(decoder, ']'); err != nil {
		return 0, err
	}
	return count, nil
}

func topLevelBareHasToken(data []byte) (bool, error) {
	entry, _, err := decodeEntry(data, "default")
	if err != nil {
		return false, err
	}
	return entryHasToken(entry), nil
}

func countCredentialMap(data []byte, limit int) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := expectJSONDelimiter(decoder, '{'); err != nil {
		return 0, err
	}
	count := 0
	members := 0
	for decoder.More() {
		members++
		if limit > 0 && members > limit+maxImportWarnings {
			return limit + 1, nil
		}
		nameToken, err := decoder.Token()
		if err != nil {
			return 0, err
		}
		name, ok := nameToken.(string)
		if !ok {
			return 0, fmt.Errorf("object member name is not a string")
		}
		if name == "accounts" || name == "credentials" {
			added, exceeded, err := countArrayFromDecoder(decoder, limit-count)
			if err != nil {
				return 0, fmt.Errorf("field %q must be an array: %w", name, err)
			}
			if exceeded {
				return limit + 1, nil
			}
			count += added
			continue
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return 0, err
		}
		if firstNonSpace(raw) != '{' || !rawEntryHasToken(raw) {
			continue
		}
		count++
		if limit > 0 && count > limit {
			return limit + 1, nil
		}
	}
	if err := finishJSONContainer(decoder, '}'); err != nil {
		return 0, err
	}
	return count, nil
}

func countArrayFromDecoder(decoder *json.Decoder, remaining int) (count int, exceeded bool, err error) {
	if err := expectJSONDelimiter(decoder, '['); err != nil {
		return 0, false, err
	}
	for decoder.More() {
		if remaining >= 0 && count >= remaining {
			return count, true, nil
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return 0, false, err
		}
		count++
	}
	if err := closeJSONContainer(decoder, ']'); err != nil {
		return 0, false, err
	}
	return count, false, nil
}

func rawEntryHasToken(raw []byte) bool {
	var tokenFields struct {
		Key          string `json:"key"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if json.Unmarshal(raw, &tokenFields) != nil {
		return false
	}
	return strings.TrimSpace(tokenFields.Key) != "" ||
		strings.TrimSpace(tokenFields.AccessToken) != "" ||
		strings.TrimSpace(tokenFields.RefreshToken) != ""
}

func expectJSONDelimiter(decoder *json.Decoder, expected json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != expected {
		return fmt.Errorf("expected %q", expected)
	}
	return nil
}

func finishJSONContainer(decoder *json.Decoder, expected json.Delim) error {
	if err := closeJSONContainer(decoder, expected); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func closeJSONContainer(decoder *json.Decoder, expected json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != expected {
		return fmt.Errorf("expected %q", expected)
	}
	return nil
}

func normalizeEntry(sourceKey string, entry GrokAuthEntry) (ImportedCredential, error) {
	// 优先 access_token；key 仅在无 access_token 时作为 CPA 兼容（部分导出把 token 放 key）
	access := strings.TrimSpace(entry.AccessToken)
	if access == "" {
		access = strings.TrimSpace(entry.Key)
	}
	refresh := strings.TrimSpace(entry.RefreshToken)
	if access == "" && refresh == "" {
		return ImportedCredential{}, fmt.Errorf("authimport: entry %q has no tokens", sourceKey)
	}
	var exp time.Time
	expiresAt := firstNonEmpty(strings.TrimSpace(entry.ExpiresAt), strings.TrimSpace(entry.Expired))
	if expiresAt != "" {
		t, err := parseFlexibleTime(expiresAt)
		if err != nil {
			return ImportedCredential{}, fmt.Errorf("authimport: entry %q expires_at: %w", sourceKey, err)
		}
		exp = t
	}
	clientID := strings.TrimSpace(entry.OIDCClientID)
	issuer := strings.TrimSpace(entry.OIDCIssuer)
	if clientID == "" || issuer == "" {
		if iss, cid, ok := splitSourceKey(strings.SplitN(sourceKey, "#entry", 2)[0]); ok {
			if issuer == "" {
				issuer = iss
			}
			if clientID == "" {
				clientID = cid
			}
		}
	}
	if issuer == "" {
		issuer = Issuer
	}
	trustedIssuer, err := NormalizeTrustedIssuer(issuer)
	if err != nil {
		return ImportedCredential{}, fmt.Errorf("authimport: entry %q oidc_issuer: %w", sourceKey, err)
	}
	issuer = trustedIssuer
	if clientID == "" {
		clientID = DefaultClientID
	}
	return ImportedCredential{
		SourceKey:    sourceKey,
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    exp,
		Email:        strings.TrimSpace(entry.Email),
		UserID:       firstNonEmpty(strings.TrimSpace(entry.UserID), strings.TrimSpace(entry.PrincipalID), strings.TrimSpace(entry.Sub)),
		TeamID:       strings.TrimSpace(entry.TeamID),
		OIDCIssuer:   issuer,
		OIDCClientID: clientID,
		AuthMode:     strings.TrimSpace(entry.AuthMode),
		Disabled:     entry.Disabled,
		ProxyURL:     firstNonEmpty(strings.TrimSpace(entry.ProxyURL), strings.TrimSpace(entry.Proxy)),
		Raw:          entry,
	}, nil
}

type objectMember struct {
	Name  string
	Value json.RawMessage
}

func decodeObjectMembers(data []byte) ([]objectMember, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("expected object")
	}
	var members []objectMember
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("object member name is not a string")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		members = append(members, objectMember{Name: name, Value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected trailing JSON")
	}
	return members, nil
}

func normalizeRawEntries(values []json.RawMessage, prefix string) ([]ImportedCredential, error) {
	out := make([]ImportedCredential, 0, len(values))
	for index, raw := range values {
		source := fmt.Sprintf("%s[%d]", prefix, index)
		entry, warnings, err := decodeEntry(raw, source)
		if err != nil {
			return nil, fmt.Errorf("authimport: %s[%d]: %w", prefix, index, err)
		}
		if !entryHasToken(entry) {
			return nil, fmt.Errorf("authimport: %s[%d] has no tokens", prefix, index)
		}
		if err := validateCPAType(source, entry); err != nil {
			return nil, err
		}
		credential, err := normalizeEntry(source, entry)
		if err != nil {
			return nil, err
		}
		credential.Warnings = warnings
		out = append(out, credential)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("authimport: no credential entries found")
	}
	return out, nil
}

var knownGrokAuthFields = map[string]struct{}{
	"type": {}, "key": {}, "access_token": {}, "auth_mode": {}, "create_time": {},
	"user_id": {}, "email": {}, "first_name": {}, "profile_image_asset_id": {},
	"principal_type": {}, "principal_id": {}, "team_id": {},
	"coding_data_retention_opt_out": {}, "refresh_token": {}, "expires_at": {},
	"expired": {}, "oidc_issuer": {}, "oidc_client_id": {}, "sub": {},
	"disabled": {}, "proxy_url": {}, "proxy": {},
}

func decodeEntry(raw []byte, source string) (GrokAuthEntry, []ImportWarning, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := expectJSONDelimiter(decoder, '{'); err != nil {
		return GrokAuthEntry{}, nil, err
	}
	fields := make(map[string]json.RawMessage, len(knownGrokAuthFields))
	unknownSet := make(map[string]struct{}, maxImportWarnings)
	omittedUnknown := false
	for decoder.More() {
		nameToken, err := decoder.Token()
		if err != nil {
			return GrokAuthEntry{}, nil, err
		}
		name, ok := nameToken.(string)
		if !ok {
			return GrokAuthEntry{}, nil, fmt.Errorf("object member name is not a string")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return GrokAuthEntry{}, nil, err
		}
		if _, known := knownGrokAuthFields[name]; known {
			fields[name] = append(json.RawMessage(nil), value...)
			continue
		}
		if len(unknownSet) < maxImportWarnings {
			unknownSet[name] = struct{}{}
		} else {
			omittedUnknown = true
		}
	}
	if err := finishJSONContainer(decoder, '}'); err != nil {
		return GrokAuthEntry{}, nil, err
	}
	knownJSON, err := json.Marshal(fields)
	if err != nil {
		return GrokAuthEntry{}, nil, err
	}
	var entry GrokAuthEntry
	if err := json.Unmarshal(knownJSON, &entry); err != nil {
		return GrokAuthEntry{}, nil, err
	}
	unknown := make([]string, 0, len(unknownSet))
	for field := range unknownSet {
		unknown = append(unknown, field)
	}
	sort.Strings(unknown)
	warnings := make([]ImportWarning, 0, len(unknown))
	for _, field := range unknown {
		warnings = append(warnings, unsupportedFieldWarning(source, field))
	}
	if omittedUnknown {
		warnings = append(warnings, ImportWarning{
			Source: source, Field: "*", Message: "additional unsupported fields omitted after warning limit",
		})
	}
	return entry, warnings, nil
}

func validateCPAType(source string, entry GrokAuthEntry) error {
	typeName := strings.ToLower(strings.TrimSpace(entry.Type))
	looksCPA := strings.TrimSpace(entry.Key) == "" && strings.TrimSpace(entry.AccessToken) != "" &&
		(strings.TrimSpace(entry.Expired) != "" || strings.TrimSpace(entry.Sub) != "")
	if typeName != "" && typeName != "xai" {
		return fmt.Errorf("authimport: entry %q field %q must be %q", source, "type", "xai")
	}
	if looksCPA && typeName != "xai" {
		return fmt.Errorf("authimport: entry %q field %q is required and must be %q for CPA credentials", source, "type", "xai")
	}
	return nil
}

func unsupportedFieldWarning(source, field string) ImportWarning {
	return ImportWarning{Source: source, Field: field, Message: "field is not supported and was ignored"}
}

func firstNonSpace(raw []byte) byte {
	for _, value := range raw {
		switch value {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return value
		}
	}
	return 0
}

func entryHasToken(entry GrokAuthEntry) bool {
	return strings.TrimSpace(entry.Key) != "" ||
		strings.TrimSpace(entry.AccessToken) != "" ||
		strings.TrimSpace(entry.RefreshToken) != ""
}

func splitSourceKey(key string) (issuer, clientID string, ok bool) {
	parts := strings.SplitN(key, "::", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	issuer = strings.TrimSpace(parts[0])
	clientID = strings.TrimSpace(parts[1])
	if issuer == "" || clientID == "" {
		return "", "", false
	}
	return issuer, clientID, true
}

func parseFlexibleTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
	}
	var last error
	for _, layout := range layouts {
		t, err := time.Parse(layout, s)
		if err == nil {
			return t.UTC(), nil
		}
		last = err
	}
	return time.Time{}, last
}

// NormalizeTrustedIssuer 仅接受生产 xAI issuer。
func NormalizeTrustedIssuer(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Issuer, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("auth issuer: invalid URL: %w", err)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	path := strings.TrimRight(u.EscapedPath(), "/")
	if u.Scheme != "https" || host != "auth.x.ai" ||
		(u.Port() != "" && u.Port() != "443") || u.User != nil ||
		path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("auth issuer: only %s is trusted", Issuer)
	}
	return Issuer, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// StableIdentity 构建确定性 identity key（与参考存储一致）。
func StableIdentity(issuer, clientID, userID, teamID, email, refreshToken, accessToken string) string {
	issuer = strings.ToLower(strings.TrimRight(strings.TrimSpace(issuer), "/"))
	clientID = strings.ToLower(strings.TrimSpace(clientID))
	userID = strings.TrimSpace(userID)
	teamID = strings.TrimSpace(teamID)
	if userID != "" {
		return "oidc:" + issuer + ":" + clientID + ":" + userID + ":" + teamID
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email != "" {
		return "email:" + issuer + ":" + clientID + ":" + email
	}
	token := strings.TrimSpace(refreshToken)
	kind := "refresh"
	if token == "" {
		token = strings.TrimSpace(accessToken)
		kind = "access"
	}
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return kind + ":" + hex.EncodeToString(sum[:])
}

// AccountIDFromIdentity 为 identity key 返回稳定的 catalog id。
func AccountIDFromIdentity(identity string) string {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(identity))
	return "acct-" + hex.EncodeToString(sum[:12])
}

// emailFromAccessToken 尽力从 JWT payload 取 email（不验签，仅导入展示用）。
func emailFromAccessToken(access string) string {
	parts := strings.Split(access, ".")
	if len(parts) < 2 {
		return ""
	}
	seg := parts[1]
	if m := len(seg) % 4; m != 0 {
		seg += strings.Repeat("=", 4-m)
	}
	raw, err := base64.URLEncoding.DecodeString(seg)
	if err != nil {
		// try RawURLEncoding
		raw, err = base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if e, ok := payload["email"].(string); ok {
		return strings.TrimSpace(e)
	}
	if e, ok := payload["preferred_username"].(string); ok && strings.Contains(e, "@") {
		return strings.TrimSpace(e)
	}
	return ""
}

// ToAccount 将导入凭证映射为适合 UpsertMany 的 catalog.Account。
// 冷存储模式缺少必要令牌时返回错误。
func ToAccount(c ImportedCredential, now time.Time) (catalog.Account, error) {
	access := strings.TrimSpace(c.AccessToken)
	refresh := strings.TrimSpace(c.RefreshToken)
	if strings.TrimSpace(c.Email) == "" {
		c.Email = emailFromAccessToken(access)
	}
	if access == "" {
		return catalog.Account{}, fmt.Errorf("authimport: missing access_token for %q", c.SourceKey)
	}
	if refresh == "" {
		return catalog.Account{}, fmt.Errorf("authimport: missing refresh_token for %q", c.SourceKey)
	}
	if now.IsZero() {
		now = time.Now()
	}
	identity := StableIdentity(c.OIDCIssuer, c.OIDCClientID, c.UserID, c.TeamID, c.Email, refresh, access)
	id := AccountIDFromIdentity(identity)
	if id == "" {
		return catalog.Account{}, fmt.Errorf("authimport: cannot derive account id for %q", c.SourceKey)
	}
	var expiresAt int64
	if !c.ExpiresAt.IsZero() {
		expiresAt = c.ExpiresAt.Unix()
	}
	proxyMode := ""
	if strings.TrimSpace(c.ProxyURL) != "" {
		proxyMode = "url"
	}
	name := firstNonEmpty(c.Email, c.UserID, c.SourceKey)
	return catalog.Account{
		ID:           id,
		Revision:     1,
		IdentityKey:  identity,
		Email:        c.Email,
		Name:         name,
		Priority:     0,
		Enabled:      !c.Disabled,
		Lifecycle:    catalog.LifecycleActive,
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    expiresAt,
		ProxyMode:    proxyMode,
		ProxyURL:     c.ProxyURL,
		CreatedAt:    now.Unix(),
		UpdatedAt:    now.Unix(),
	}, nil
}

// ParseFileBytes 为批量导入 JSON 文档的便捷封装。
func ParseFileBytes(data []byte) ([]catalog.Account, []ImportWarning, error) {
	creds, warnings, err := ParseGrokAuthJSONDetailed(data)
	if err != nil {
		return nil, warnings, err
	}
	now := time.Now()
	out := make([]catalog.Account, 0, len(creds))
	for _, c := range creds {
		a, err := ToAccount(c, now)
		if err != nil {
			return nil, warnings, err
		}
		out = append(out, a)
	}
	return out, warnings, nil
}
