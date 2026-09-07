#!/usr/bin/env python3
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""
Autonomous bug-hunting driver for Agent Substrate (pred-ate).
Runs inside an ephemeral QEMU VM to explore code, test novel hypotheses,
and generate structured reports without human intervention.
"""

import asyncio
import json
import os
import re
import sys
from pathlib import Path

KNOWN_FINDINGS_DIR = Path("/mnt/known_findings")
OUTPUT_DIR = Path("/mnt/output")
WORKSPACE_DIR = Path("/workspace/substrate")

# Pinned explicitly rather than relying on the SDK's own default (currently
# also "gemini-3.8-flash"), so a future google-antigravity upgrade can't
# silently change which model this runs against.
MODEL = "gemini-3.8-flash"

SYSTEM_PROMPT = """
You are an autonomous reliability and security bug-hunting agent for Agent Substrate ("ate").
Mascot: YOLO the octopus. Your tool name is pred-ate.
You operate completely autonomously inside an isolated Linux sandbox VM with full root privileges.
All commands executed via run_command and file edits are automatically executed.

Your goal is to formulate a novel hypothesis about an edge case, race condition, or failure mode
in Agent Substrate, write a minimal reproducible test script, execute it against a local Kind cluster,
verify whether it is an actual bug or verified resilient behavior, and record your findings.

Guidelines:
- Rigorous verification: Every finding must be backed by concrete script execution and log evidence.
- Novelty: Do NOT test edge cases that are already documented as known findings or verified resilient.
- Clear verdict: Classify the run as CONFIRMED_BUG, VERIFIED_RESILIENT, or INCONCLUSIVE.
"""


def load_known_findings() -> str:
    """Reads all known findings from /mnt/known_findings to feed into the prompt."""
    if not KNOWN_FINDINGS_DIR.exists():
        return "No prior findings recorded."

    findings = []
    for f in KNOWN_FINDINGS_DIR.glob("**/*.md"):
        if f.name == "README.md":
            continue
        try:
            content = f.read_text(encoding="utf-8")
            # Extract frontmatter if present
            match = re.search(r"^---\n(.*?)\n---", content, re.DOTALL)
            summary = match.group(1) if match else content[:200]
            findings.append(f"[{f.parent.name}/{f.name}]:\n{summary}\n")
        except Exception as e:
            findings.append(f"[{f.name}]: error reading ({e})")

    if not findings:
        return "No prior findings recorded yet."
    return "\n".join(findings)


async def run_phase(agent, title: str, prompt: str):
    """Executes a single phase, streaming each event to stdout as it occurs.

    A single agent.chat() call can span the whole agentic loop for this phase
    (think, call a tool that takes minutes, see the result, think again, ...).
    Draining response.thoughts/.tool_calls/the text stream one at a time --
    each of which only finishes once the *entire* turn is done -- would print
    nothing at all until the whole phase completes. Iterating response.chunks
    directly instead prints every event in the order it actually happens.
    """
    from google.antigravity.types import Thought, Text, ToolCall, ToolResult

    print(f"\n{'='*25} {title} {'='*25}\n", flush=True)
    response = await agent.chat(prompt)

    async for chunk in response.chunks:
        if isinstance(chunk, Thought):
            print(f"[Thinking] {chunk.text}", flush=True)
        elif isinstance(chunk, ToolCall):
            cmd_info = (
                chunk.args.get("cmd")
                or chunk.args.get("CommandLine")
                or chunk.args.get("TargetFile")
                or chunk.args
            )
            print(f"\n[Tool Execution] {chunk.name}: {cmd_info}\n", flush=True)
        elif isinstance(chunk, ToolResult):
            status = "ERROR" if chunk.error else "OK"
            output = chunk.error or chunk.result
            if isinstance(output, (dict, list)):
                output = json.dumps(output, indent=2)
            print(f"\n[Tool Result] {chunk.name} ({status}):\n{output}\n", flush=True)
        elif isinstance(chunk, Text):
            sys.stdout.write(chunk.text)
            sys.stdout.flush()
    print("\n", flush=True)

    stop_reason = response.stop_reason
    if stop_reason.value != "UNSPECIFIED":
        print(f"[pred-ate] NOTE: phase stopped early - {stop_reason.value}", flush=True)


async def main():
    try:
        from google.antigravity import Agent, LocalAgentConfig, CapabilitiesConfig, policy
        from google.antigravity.types import BudgetConfig, ModelAPIRetryConfig, RetryConfig
    except ImportError:
        print("ERROR: google-antigravity Python package is not installed.", file=sys.stderr)
        sys.exit(1)

    known_findings_summary = load_known_findings()
    hint = os.environ.get("PREDATE_HINT", "").strip()

    budget_kwargs = {}
    for env_var, field in (
        ("PREDATE_MAX_TOTAL_TOKENS", "max_total_tokens"),
        ("PREDATE_MAX_INPUT_TOKENS", "max_input_tokens"),
        ("PREDATE_MAX_OUTPUT_TOKENS", "max_output_tokens"),
    ):
        value = os.environ.get(env_var, "").strip()
        if value and int(value) > 0:
            budget_kwargs[field] = int(value)

    budget_config = BudgetConfig(**budget_kwargs) if budget_kwargs else None
    if budget_config:
        limits = ", ".join(f"{k}={v:,}" for k, v in budget_kwargs.items())
        print(f"[*] Session token budget: {limits}", flush=True)

    hint_section = ""
    if hint:
        print(f"[*] Operator guidance detected: '{hint}'", flush=True)
        hint_section = (
            f"*** OPERATOR GUIDANCE / SUSPICION ***\n"
            f"The human operator specifically requested that you investigate this area or suspicion:\n"
            f"> \"{hint}\"\n"
            f"Please prioritize formulating a hypothesis around this area or subsystem.\n\n"
        )

    phases = [
        (
            "PHASE 1: INGEST PRIOR FINDINGS & FORMULATE NOVEL HYPOTHESIS",
            f"{hint_section}"
            f"You are in `/workspace/substrate`, with a Kind cluster already running and "
            f"ate-system already deployed and ready (verify with `kubectl get pods -n ate-system` "
            f"if you want to confirm).\n\n"
            f"Here is the database of prior findings already tested:\n\n"
            f"```\n{known_findings_summary}\n```\n\n"
            "Task:\n"
            "1. Review the above prior findings. You MUST NOT re-test any of these behaviors.\n"
            "2. Inspect the Substrate codebase (e.g. `cmd/ateapi/`, `cmd/atelet/`, `cmd/atenet/`, `internal/`).\n"
            "3. Identify an unexplored hypothesis" + (f" focused on: '{hint}'" if hint else "") + ".\n"
            "4. State your hypothesis clearly and explain how you will test it."
        ),
        (
            "PHASE 2: IMPLEMENT REPRODUCER",
            "Write a standalone reproducer script or test program at `/workspace/reproducer.sh` (or `.go`).\n"
            "The reproducer must:\n"
            "- Set up any required resources (e.g. via `kubectl ate create ...` or `curl`).\n"
            "- Trigger the exact stress/edge case condition.\n"
            "- Print clear markers: `=== TEST RESULT: PASS ===` or `=== TEST RESULT: FAIL ===`.\n"
            "Make it executable with `chmod +x /workspace/reproducer.sh`."
        ),
        (
            "PHASE 3: EXECUTE & DIAGNOSE",
            "1. Run `/workspace/reproducer.sh`.\n"
            "2. Stream and collect diagnostic logs:\n"
            "   `kubectl logs -n ate-system -l app=ate-api-server --tail=200`\n"
            "   `kubectl logs -n ate-system -l app=atelet --tail=200`\n"
            "   `kubectl get workers,actors -n ate-system` (or relevant atespace)\n"
            "3. Analyze whether the system panicked, deadlocked, leaked resources, or handled it gracefully."
        ),
        (
            "PHASE 4: DOCUMENT FINDING",
            "Analyze your test results and write a final report to `/mnt/output/finding.md`.\n\n"
            "The report MUST start with YAML frontmatter in this exact schema:\n"
            "```yaml\n"
            "---\n"
            "verdict: CONFIRMED_BUG  # or VERIFIED_RESILIENT or INCONCLUSIVE\n"
            "subsystem: cmd/ateapi   # primary subsystem tested\n"
            "components: [store, scheduler]\n"
            "tested_hypothesis: >\n"
            "  One-line summary of what hypothesis was tested\n"
            "severity: HIGH          # LOW, MEDIUM, HIGH, CRITICAL (only if CONFIRMED_BUG)\n"
            "---\n"
            "```\n\n"
            "Follow the frontmatter with:\n"
            "## Summary\n"
            "## Expected vs Actual Behavior\n"
            "## Step-by-Step Reproduction\n"
            "## Diagnostic Logs\n"
            "## Recommended Code Fix (if bug) or Takeaway (if resilient)\n\n"
            "Finally, copy your reproducer script to `/mnt/output/reproducer.sh`."
        ),
    ]

    # The SDK's default API retry budget is tuned for interactive use (a human
    # can just retry by hand), so a transient 503 "model overloaded" error
    # exhausts it and raises AntigravityExecutionError, killing this
    # unattended session outright. Give it a much larger (but still bounded --
    # this VM has no overall run timeout) retry budget with exponential
    # backoff instead.
    retry_config = RetryConfig(
        api_retry=ModelAPIRetryConfig(
            max_retries=8,
            initial_sleep_duration_ms=3000,
            exponential_multiplier=2.0,
            jitter_range=0.2,
        )
    )

    # This VM is headless with no human present, so every tool (including
    # run_command, which is confirm-gated by default) must be pre-approved --
    # otherwise the agent blocks forever waiting for an approval that will
    # never come.
    config = LocalAgentConfig(
        system_instructions=SYSTEM_PROMPT,
        capabilities=CapabilitiesConfig(),
        policies=[policy.allow_all()],
        retry_config=retry_config,
        budget_config=budget_config,
        model=MODEL,
    )

    async with Agent(config) as agent:
        for title, prompt in phases:
            await run_phase(agent, title, prompt)

        usage = agent.conversation.total_usage
        print(f"\n{'='*25} TOKEN USAGE SUMMARY {'='*25}\n", flush=True)
        print(f"  Prompt tokens:     {usage.prompt_token_count}", flush=True)
        print(f"  Cached tokens:     {usage.cached_content_token_count}", flush=True)
        print(f"  Candidate tokens:  {usage.candidates_token_count}", flush=True)
        print(f"  Thinking tokens:   {usage.thoughts_token_count}", flush=True)
        print(f"  TOTAL tokens used: {usage.total_token_count}", flush=True)

    print("\n[pred-ate] Autonomous run completed successfully.", flush=True)


if __name__ == "__main__":
    asyncio.run(main())
