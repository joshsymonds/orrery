package transport

import (
	"encoding/json"
	"fmt"

	"github.com/joshsymonds/orrery/internal/engine/wire"
)

// SendSetPermissionMode writes one caller-correlated request that switches the permission mode.
func (transport *Transport) SendSetPermissionMode(requestID, mode string) error {
	transport.writeMu.Lock()
	defer transport.writeMu.Unlock()

	request, err := json.Marshal(wire.SDKControlSetPermissionModeRequest{
		Subtype: "set_permission_mode",
		Mode:    mode,
	})
	if err != nil {
		return fmt.Errorf("transport: marshal set permission mode request: %w", err)
	}
	encodedID, err := json.Marshal(requestID)
	if err != nil {
		return fmt.Errorf("transport: marshal set permission mode request ID: %w", err)
	}
	envelope := make([]byte, 0, len(encodedID)+len(request)+64)
	envelope = append(envelope, `{"type":"control_request","request_id":`...)
	envelope = append(envelope, encodedID...)
	envelope = append(envelope, `,"request":`...)
	envelope = append(envelope, request...)
	envelope = append(envelope, '}')
	return transport.writeLocked(append(envelope, '\n'))
}

// SendPermissionAllow answers a can_use_tool request with an allow decision. updatedInput
// replaces the tool input and must be valid JSON; invalid input is rejected before any write.
func (transport *Transport) SendPermissionAllow(
	requestID string,
	updatedInput json.RawMessage,
) error {
	body, err := json.Marshal(wire.SDKPermissionAllow{
		Behavior:     "allow",
		UpdatedInput: updatedInput,
	})
	if err != nil {
		return fmt.Errorf("transport: marshal permission allow: %w", err)
	}
	return transport.SendControlResponse(requestID, body)
}

// SendPermissionDeny answers a can_use_tool request with a deny decision and a message for the user.
func (transport *Transport) SendPermissionDeny(requestID, message string) error {
	body, err := json.Marshal(wire.SDKPermissionDeny{
		Behavior: "deny",
		Message:  message,
	})
	if err != nil {
		return fmt.Errorf("transport: marshal permission deny: %w", err)
	}
	return transport.SendControlResponse(requestID, body)
}
