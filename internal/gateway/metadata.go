package gateway

import "encoding/json"

// maxObservedIDBytes bounds an observed account or session ID.
const maxObservedIDBytes = 256

// Caller reads the subscription account and the session Claude Code names in a request body's metadata.user_id. That
// value is itself a JSON object encoded as a string: {"device_id":…,"account_uuid":…,"session_id":…}. A body of any
// other shape, or an identifier that is not a printable one, names nobody, and the empty string stands for it.
func Caller(body []byte) (accountUUID, sessionID string) {
	fields, ok := decodeObject(body)
	if !ok {
		return "", ""
	}

	metadata, ok := decodeObject(fields["metadata"])
	if !ok {
		return "", ""
	}

	userID, ok := decodeString(metadata["user_id"])
	if !ok || userID == "" {
		return "", ""
	}

	identity, ok := decodeObject([]byte(userID))
	if !ok {
		return "", ""
	}

	account, accountOK := optionalString(identity, "account_uuid")
	session, sessionOK := optionalString(identity, "session_id")
	if !accountOK || !sessionOK {
		return "", ""
	}

	return observedID(account), observedID(session)
}

// optionalString decodes a string field that may be absent or null. A field that is present and not a string fails.
func optionalString(fields map[string]json.RawMessage, name string) (string, bool) {
	raw, found := fields[name]
	if !found || isNull(raw) {
		return "", true
	}

	return decodeString(raw)
}

// observedID returns value when it is a header-observable identifier: 1 to 256 bytes of printable, non-space ASCII.
// Anything else names nobody, and comes back empty.
func observedID(value string) string {
	if len(value) < 1 || len(value) > maxObservedIDBytes {
		return ""
	}

	for index := range len(value) {
		if value[index] < 0x21 || value[index] > 0x7e {
			return ""
		}
	}

	return value
}
