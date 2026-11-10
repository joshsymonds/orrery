package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestFixturesResanitiseUnchanged checks that the committed fixtures are a fixed point of the
// sanitiser: running each probe's fixtures through it again returns the same bytes.
func TestFixturesResanitiseUnchanged(t *testing.T) {
	t.Parallel()
	raw, err := os.OpenRoot(fixtureDir)
	if err != nil {
		t.Fatalf("open %s: %v", fixtureDir, err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	out, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatalf("open temporary directory: %v", err)
	}
	t.Cleanup(func() { _ = out.Close() })
	for _, probe := range Probes() {
		if probeErr := cliSanitiseProbe(probe.Name, raw, out); probeErr != nil {
			t.Fatalf("sanitize %s: %v", probe.Name, probeErr)
		}
		for _, suffix := range []string{".in.ndjson", ".out.ndjson"} {
			name := probe.Name + suffix
			committed, readErr := os.ReadFile(filepath.Join(fixtureDir, name))
			if readErr != nil {
				t.Fatalf("read committed %s: %v", name, readErr)
			}
			again, readErr := out.ReadFile(name)
			if readErr != nil {
				t.Fatalf("read re-sanitized %s: %v", name, readErr)
			}
			if !bytes.Equal(again, committed) {
				t.Errorf("%s changes when sanitized again", name)
			}
		}
	}
}

// TestFixturesKeysAreProtocolNames checks that every object key at every depth of every fixture line is
// a protocol field name from fixtureProtocolKeys or a key_<digits> placeholder.
func TestFixturesKeysAreProtocolNames(t *testing.T) {
	t.Parallel()
	allowed := make(map[string]bool)
	for _, key := range strings.Fields(fixtureProtocolKeys) {
		allowed[key] = true
	}
	placeholder := regexp.MustCompile(`^key_[0-9]+$`)
	var problems []string
	for _, probe := range Probes() {
		for _, suffix := range []string{".in.ndjson", ".out.ndjson"} {
			name := probe.Name + suffix
			data, err := os.ReadFile(filepath.Join(fixtureDir, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			for index, line := range bytes.Split(data, []byte("\n")) {
				if len(line) == 0 {
					continue
				}
				where := fmt.Sprintf("%s:%d", name, index+1)
				var value any
				if err := json.Unmarshal(line, &value); err != nil {
					t.Fatalf("%s:%d: decode: %v", name, index+1, err)
				}
				for _, key := range fixtureObjectKeys(value, nil) {
					if !allowed[key] && !placeholder.MatchString(key) {
						problems = append(problems, fmt.Sprintf("%s: key %q", where, key))
					}
				}
			}
		}
	}
	if len(problems) > 0 {
		t.Errorf("fixtures in %s hold keys outside the protocol list:\n%s", fixtureDir, strings.Join(problems, "\n"))
	}
}

// fixtureObjectKeys appends every object key at every depth of value to keys.
func fixtureObjectKeys(value any, keys []string) []string {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			keys = append(keys, key)
			keys = fixtureObjectKeys(child, keys)
		}
	case []any:
		for _, child := range typed {
			keys = fixtureObjectKeys(child, keys)
		}
	}
	return keys
}

// fixtureProtocolKeys lists the protocol field names the regenerated fixtures use. A key outside this
// list, other than a key_<digits> placeholder, means a sanitiser let a non-protocol name through.
const fixtureProtocolKeys = `
	abandoned_blocks aborted account additional_directories agents analytics_disabled answers
	api_error_status apiKeySource api_message_id applied_edits argumentHint auto
	available_output_styles background behavior budget builtin by_type cache_creation
	cacheCreationEphemeral1hInputTokens cacheCreationEphemeral5mInputTokens
	cache_creation_input_tokens cacheCreationInputTokens cache_missed_input_tokens
	cache_miss_reason cache_read_input_tokens cacheReadInputTokens caller canonicalModel
	capabilities claude_code_version commands command_uuid completed concurrency_limit container
	content content_block context_management contextWindow costBasis costUSD
	current_permission_mode cwd decision delta depth_limit description destination diagnostics
	display_name displayName duration_api_ms duration_ms ephemeral_1h_input_tokens
	ephemeral_5m_input_tokens errors estimated_tokens estimated_tokens_delta event failed
	fallback_credit fast_mode_disabled_reason fast_mode_state feedback_mode file_path filePath
	first_content_frame_ms first_request_cache_read_input_tokens first_request_input_tokens
	foreground from_block_index header hook_on_failure_supported hooks hooks_applied id
	ide_rc_auto_enable_gate index inference_geo input input_tokens inputTokens
	input_transformations is_error iterations killed kind label max_depth maxOutputTokens
	mcp_servers memory_paths message messaging_socket_path mode model models modelUsage
	model_usage_since_last_result multiSelect name non_execution_kind num_turns options
	original_cwd originalFile other_hook_ask_outranks_host output_style output_tokens outputTokens
	output_tokens_details parent parent_tool_use_id partial_json path pending_permission_requests
	pending_user_dialog_requests permission_decision permission_denials permissionMode
	permission_suggestions per_turn_effort_active pid plugins product_feedback_disabled provider
	question questions queued_turn_count refused remote_control_auto_connect_default
	remote_control_auto_enable remote_control_auto_on_by_default remote_control_available request
	requested request_id request_sent_wall_ms requires_user_interaction resolvedModel response
	result result_index role safety_stops server_tool_use service_tier session_id session_state
	signature skills slash_commands source spawned spawned_by_subagents speed started_in_background
	state status still_queued stop_details stop_reason stop_sequence structuredPatch subagent_stats
	subtype supportedDialogKinds supportedEffortLevels supportsAdaptiveThinking supportsAutoMode
	supportsEffort supportsFastMode supportsThinkingOff system systemPrompt terminal_reason
	terminal_slash_commands text thinking thinking_display thinking_duration_ms thinking_tokens
	thinkingTokens timestamp time_to_request_ms tool_input tool_name tool_result_meta tools
	tool_use_id tool_use_result total_cost_usd ttft_ms ttft_stream_ms type ultrathink_available
	ultrathink_typed_raises unset updatedInput usage user user_message_uuid user_message_uuids
	userModified user_output_styles_dir uuid value view_mode web_fetch_requests web_search_requests
	webSearchRequests wire_tool_inputs
`
