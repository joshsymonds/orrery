#!/bin/sh
# fakeclaude.sh stands in for the claude binary in the engine host tests. It writes its pid to fakeclaude.pid in its
# working directory, then answers each user prompt with a scripted turn. The prompt number n counts from 1. A prompt
# containing ASK asks one AskUserQuestion, PERMIT asks for a Write permission, HANG streams text until an interrupt,
# FLOOD streams 50 finished text blocks, TRICKLE streams 60 finished text blocks about 50 ms apart, STALL streams the
# start of a text block and never finishes it, TERMPROOF ignores SIGTERM and then stalls as STALL does, DIE streams
# the same start, writes fake died to stderr and exits with status 3, BACKGROUND starts a background task and ends
# its turn while the task runs, BGEND ends that task and then answers as any other prompt does, PAIR answers with one
# assistant line that carries two finished text blocks, "fake pair n.1" and "fake pair n.2", and any other prompt
# streams "fake reply n". The turns reproduce the stream lines captured from the real CLI, with only text, ids and the
# session id changed. Every line it reads from stdin, prompts and answers alike, is appended to fakeclaude.stdin as it
# is read. It exits at stdin EOF.
#
# Its init line lists the --agent it was given, or nothing when it was given none. A prompt that contains NOAGENT gets an
# init line that lists nothing, as a claude that did not load the agent would.

# assistant_line CONTENT: the assistant line that carries CONTENT, the JSON of its finished content blocks.
assistant_line() {
	printf '%s\n' '{"type":"assistant","message":{"model":"claude-haiku-5-5","id":"msg_1","type":"message","role":"assistant","content":['"$1"'],"container":null,"stop_reason":null,"stop_sequence":null,"stop_details":null,"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":0,"service_tier":"redacted","inference_geo":"redacted","speed":"redacted"},"input_transformations":[],"diagnostics":null,"context_management":null},"parent_tool_use_id":null,"session_id":"session_1","uuid":"uuid_9","timestamp":"redacted","request_id":"req_2","user_message_uuid":"uuid_1","user_message_uuids":["uuid_1"]}'
}

# text_block INDEX FULL DELTA...: a text block that streams each DELTA, then finishes as FULL.
text_block() {
	index=$1
	full=$2
	shift 2
	printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_start","index":'"$index"',"content_block":{"type":"text","text":""}},"session_id":"session_1","parent_tool_use_id":null,"uuid":"uuid_7","api_message_id":"redacted"}'
	for delta in "$@"; do
		printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":'"$index"',"delta":{"type":"text_delta","text":"'"$delta"'"}},"session_id":"session_1","parent_tool_use_id":null,"uuid":"uuid_8","api_message_id":"redacted"}'
	done
	assistant_line '{"type":"text","text":"'"$full"'"}'
	printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_stop","index":'"$index"'},"session_id":"session_1","parent_tool_use_id":null,"uuid":"uuid_10","api_message_id":"redacted"}'
}

# next reads one stdin line into line, appends it to fakeclaude.stdin as read, and fails at EOF.
next() {
	IFS= read -r line || return 1
	printf '%s\n' "$line" >> fakeclaude.stdin
}

# turn_open starts a turn: the system init, the status and the message start. The init lists $agent, or nothing when
# omit_agent is set for the prompt that opened the turn.
turn_open() {
	agents='[]'
	if [ -n "$agent" ] && [ -z "$omit_agent" ]; then
		agents='["'"$agent"'"]'
	fi
	printf '%s\n' '{"type":"system","subtype":"init","cwd":"redacted","session_id":"session_1","tools":[],"mcp_servers":[],"model":"claude-haiku-5-5","permissionMode":"default","slash_commands":["redacted"],"terminal_slash_commands":["redacted"],"apiKeySource":"redacted","claude_code_version":"2.1.321","output_style":"redacted","agents":'"$agents"',"skills":["redacted"],"plugins":[{"name":"redacted","path":"redacted","source":"redacted"}],"capabilities":["redacted"],"analytics_disabled":false,"product_feedback_disabled":false,"uuid":"uuid_4","memory_paths":{"auto":"redacted"},"original_cwd":"redacted","additional_directories":[],"messaging_socket_path":"redacted","fast_mode_state":"redacted","fast_mode_disabled_reason":"redacted","per_turn_effort_active":false,"ultrathink_available":false,"ultrathink_typed_raises":false,"view_mode":"redacted","user_message_uuids":["uuid_1"]}'
	printf '%s\n' '{"type":"system","subtype":"status","status":"requesting","session_id":"session_1","uuid":"uuid_5","user_message_uuids":["uuid_1"]}'
	printf '%s\n' '{"type":"stream_event","event":{"type":"message_start","message":{"model":"claude-haiku-5-5","id":"msg_1","type":"message","role":"assistant","content":[],"container":null,"stop_reason":null,"stop_sequence":null,"stop_details":null,"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":0,"service_tier":"redacted","inference_geo":"redacted","speed":"redacted"},"input_transformations":[],"diagnostics":null,"context_management":null}},"session_id":"session_1","parent_tool_use_id":null,"uuid":"uuid_6","ttft_ms":0,"thinking_display":"redacted","api_message_id":"redacted","user_message_uuid":"uuid_1","user_message_uuids":["uuid_1"]}'
}

# turn_close ends a completed turn: the message delta, the message stop and the success result.
turn_close() {
	printf '%s\n' '{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null,"stop_details":null,"container":null},"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0,"output_tokens_details":{"thinking_tokens":0},"iterations":[{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"type":"redacted"}]},"context_management":{"applied_edits":[]}},"session_id":"session_1","parent_tool_use_id":null,"uuid":"uuid_11","api_message_id":"redacted"}'
	printf '%s\n' '{"type":"stream_event","event":{"type":"message_stop"},"session_id":"session_1","parent_tool_use_id":null,"uuid":"uuid_12","api_message_id":"redacted"}'
	printf '%s\n' '{"duration_api_ms":0,"stop_reason":"end_turn","session_id":"session_1","total_cost_usd":0,"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0,"output_tokens_details":{"thinking_tokens":0},"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"redacted","cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"inference_geo":"redacted","iterations":[{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"type":"redacted"}],"speed":"redacted","fallback_credit":null},"modelUsage":{"key_1":{"inputTokens":0,"outputTokens":0,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"webSearchRequests":0,"costUSD":0,"contextWindow":0,"maxOutputTokens":0,"thinkingTokens":0,"cacheCreationEphemeral5mInputTokens":0,"cacheCreationEphemeral1hInputTokens":0,"canonicalModel":"redacted","provider":"redacted","costBasis":"redacted"}},"permission_denials":[],"terminal_reason":"completed","fast_mode_state":"redacted","fast_mode_disabled_reason":"redacted","subagent_stats":{"spawned":0,"requested":{"background":0,"foreground":0,"unset":0},"started_in_background":0,"max_depth":0,"spawned_by_subagents":0,"completed":0,"failed":0,"killed":{"parent":0,"user":0,"system":0},"refused":{"depth_limit":0,"concurrency_limit":0,"budget":0},"by_type":{}},"safety_stops":0,"is_error":false,"num_turns":1,"subtype":"success","api_error_status":null,"result":"redacted","ttft_ms":0,"type":"result","duration_ms":0,"uuid":"uuid_13","ttft_stream_ms":0,"time_to_request_ms":0,"first_content_frame_ms":0,"user_message_uuid":"uuid_1","user_message_uuids":["uuid_1"],"request_sent_wall_ms":0,"first_request_input_tokens":0,"first_request_cache_read_input_tokens":0,"queued_turn_count":0,"result_index":0,"model_usage_since_last_result":{"key_1":{"inputTokens":0,"outputTokens":0,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"webSearchRequests":0,"costUSD":0,"contextWindow":0,"maxOutputTokens":0,"thinkingTokens":0,"cacheCreationEphemeral5mInputTokens":0,"cacheCreationEphemeral1hInputTokens":0,"canonicalModel":"redacted","provider":"redacted","costBasis":"redacted"}}}'
}

# reply streams "fake reply n" in two deltas and ends the turn.
reply() {
	turn_open
	text_block 0 "fake reply $n" "fake " "reply $n"
	turn_close
}

# ask sends an AskUserQuestion request, waits for the host's answer, then reports it.
ask() {
	turn_open
	printf '%s\n' '{"type":"control_request","request_id":"ask-'"$n"'","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","display_name":"AskUserQuestion","input":{"questions":[{"question":"Which color?","header":"Color","options":[{"label":"red","description":"red"},{"label":"blue","description":"blue"}],"multiSelect":false}]},"tool_use_id":"toolu_ask_'"$n"'","requires_user_interaction":true}}'
	next
	printf '%s\n' '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"Which color? red","tool_use_id":"toolu_ask_'"$n"'"}]},"parent_tool_use_id":null,"session_id":"session_1","uuid":"uuid_26","timestamp":"redacted","tool_use_result":{"questions":[{"question":"Which color?","header":"Color","options":[{"label":"red","description":"red"},{"label":"blue","description":"blue"}],"multiSelect":false}],"answers":{"Which color?":"red"}},"tool_result_meta":[{"id":"redacted","permission_decision":{"decision":"allow","source":"redacted"}}]}'
	text_block 0 "fake answered $n" "fake answered $n"
	turn_close
}

# permit sends a Write permission request, waits for the host's decision, then reports it.
permit() {
	turn_open
	printf '%s\n' '{"type":"control_request","request_id":"permit-'"$n"'","request":{"subtype":"can_use_tool","tool_name":"Write","display_name":"Write","input":{"file_path":"fake.txt","content":"fake"},"description":"write fake.txt","permission_suggestions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}],"tool_use_id":"toolu_permit_'"$n"'"}}'
	next
	case $line in
		*'"behavior":"allow"'*) text_block 0 "fake allowed $n" "fake allowed $n" ;;
		*) text_block 0 "fake denied $n" "fake denied $n" ;;
	esac
	turn_close
}

# hang streams text, then waits until an interrupt ends the turn with an error result.
hang() {
	turn_open
	text_block 0 "fake hanging $n" "fake hanging $n"
	while next; do
		case $line in
			*'"subtype":"interrupt"'*) break ;;
		esac
	done
	printf '%s\n' '{"duration_api_ms":0,"stop_reason":null,"session_id":"session_1","total_cost_usd":0,"usage":{"output_tokens_details":{"thinking_tokens":0},"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"redacted","cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"inference_geo":"redacted","iterations":[],"speed":"redacted","fallback_credit":null},"modelUsage":{},"permission_denials":[],"terminal_reason":"aborted_streaming","fast_mode_state":"redacted","fast_mode_disabled_reason":"redacted","subagent_stats":{"spawned":0,"requested":{"background":0,"foreground":0,"unset":0},"started_in_background":0,"max_depth":0,"spawned_by_subagents":0,"completed":0,"failed":0,"killed":{"parent":0,"user":0,"system":0},"refused":{"depth_limit":0,"concurrency_limit":0,"budget":0},"by_type":{}},"safety_stops":0,"is_error":true,"num_turns":2,"subtype":"error_during_execution","errors":["redacted"],"user_message_uuid":"uuid_1","type":"result","duration_ms":0,"uuid":"uuid_14","user_message_uuids":["uuid_1"],"queued_turn_count":0,"result_index":0,"model_usage_since_last_result":{}}'
}

# flood streams 50 finished text blocks and ends the turn.
flood() {
	turn_open
	k=1
	while [ "$k" -le 50 ]; do
		text_block $((k - 1)) "fake flood $n.$k" "fake flood $n.$k"
		k=$((k + 1))
	done
	turn_close
}

# trickle streams 60 finished text blocks, about 50 ms apart, and ends the turn.
trickle() {
	turn_open
	k=1
	while [ "$k" -le 60 ]; do
		text_block $((k - 1)) "fake trickle $n.$k" "fake trickle $n.$k"
		sleep 0.05
		k=$((k + 1))
	done
	turn_close
}

# stall streams the start of a text block, never finishes it, and reads stdin until it ends.
stall() {
	turn_open
	printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}},"session_id":"session_1","parent_tool_use_id":null,"uuid":"uuid_7","api_message_id":"redacted"}'
	printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"fake stalling '"$n"'"}},"session_id":"session_1","parent_tool_use_id":null,"uuid":"uuid_8","api_message_id":"redacted"}'
	while next; do
		:
	done
}

# termproof ignores SIGTERM, then stalls as stall does, so only a SIGKILL ends it.
termproof() {
	trap '' TERM
	stall
}

# background starts a background task, streams a text block and ends the turn while the task still runs.
background() {
	turn_open
	printf '%s\n' '{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"task_fake_1","task_type":"local_bash","description":"fake sleep"}],"session_id":"session_1","uuid":"uuid_t1"}'
	text_block 0 "fake background $n" "fake background $n"
	turn_close
}

# bgend reports that the background task has ended, then answers as any other prompt does.
bgend() {
	printf '%s\n' '{"type":"system","subtype":"background_tasks_changed","tasks":[],"session_id":"session_1","uuid":"uuid_t2"}'
	reply
}

# pair answers with one assistant line that carries two finished text blocks, so one stdout line yields two events.
pair() {
	turn_open
	assistant_line '{"type":"text","text":"fake pair '"$n"'.1"},{"type":"text","text":"fake pair '"$n"'.2"}'
	turn_close
}

# die streams the start of a text block and one delta of it, writes fake died to stderr and exits with status 3.
die() {
	turn_open
	printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}},"session_id":"session_1","parent_tool_use_id":null,"uuid":"uuid_7","api_message_id":"redacted"}'
	printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"fake dying '"$n"'"}},"session_id":"session_1","parent_tool_use_id":null,"uuid":"uuid_8","api_message_id":"redacted"}'
	printf '%s\n' 'fake died' >&2
	exit 3
}

echo $$ > fakeclaude.pid.tmp && mv fakeclaude.pid.tmp fakeclaude.pid
printf '%s\n' "$*" >> fakeclaude.args

# A start with --resume continues the prompt count of the earlier start, and only resumes session_1. Its init lists the
# --agent of the start.
resume=
agent=
prev=
for arg in "$@"; do
	if [ "$prev" = --resume ]; then
		resume=$arg
	fi
	if [ "$prev" = --agent ]; then
		agent=$arg
	fi
	prev=$arg
done
n=0
if [ -n "$resume" ]; then
	if [ "$resume" != session_1 ]; then
		echo "fake claude: no conversation with id $resume" >&2
		exit 1
	fi
	n=$(cat fakeclaude.n 2>/dev/null || echo 0)
fi
while next; do
	case $line in
		*'"type":"user"'*)
			n=$((n + 1))
			printf '%s\n' "$n" > fakeclaude.n
			omit_agent=
			case $line in
				*NOAGENT*) omit_agent=1 ;;
			esac
			case $line in
				*ASK*) ask ;;
				*PERMIT*) permit ;;
				*HANG*) hang ;;
				*FLOOD*) flood ;;
				*TRICKLE*) trickle ;;
				*STALL*) stall ;;
				*TERMPROOF*) termproof ;;
				*DIE*) die ;;
				*BACKGROUND*) background ;;
				*BGEND*) bgend ;;
				*PAIR*) pair ;;
				*) reply ;;
			esac
			;;
	esac
done
