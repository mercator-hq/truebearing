# Using TrueBearing with LangChain

LangChain's HTTP tool client and ChatOpenAI wrapper both support a `base_url` override.
Point them at `http://localhost:7773/mcp/v1` and add the two required headers. No other
change is needed.

## Prerequisites

Complete the [Quick Start](../../README.md#quick-start-confirmed-blocked-call-in-under-10-minutes)
steps first:

1. `truebearing agent register my-agent --policy ./truebearing.policy.yaml`
2. `truebearing serve --upstream <mcp-url> --policy ./truebearing.policy.yaml`
3. Export the agent JWT: `export TRUEBEARING_AGENT_JWT=$(cat ~/.truebearing/keys/my-agent.jwt)`

## Python (`langchain-openai`)

```python
import os
import uuid
from langchain_openai import ChatOpenAI

session_id = str(uuid.uuid4())

llm = ChatOpenAI(
    model="gpt-4o",
    openai_api_base="http://localhost:7773/mcp/v1",
    default_headers={
        "Authorization": f"Bearer {os.environ['TRUEBEARING_AGENT_JWT']}",
        "X-TrueBearing-Session-ID": session_id,
    },
)

# All tool calls made through this LLM instance are policy-enforced.
result = llm.invoke("Process the pending record.")
```

## Python (`langchain-anthropic`)

```python
import os
import uuid
from langchain_anthropic import ChatAnthropic

session_id = str(uuid.uuid4())

llm = ChatAnthropic(
    model="claude-sonnet-4-6",
    anthropic_api_url="http://localhost:7773/mcp/v1",
    default_headers={
        "Authorization": f"Bearer {os.environ['TRUEBEARING_AGENT_JWT']}",
        "X-TrueBearing-Session-ID": session_id,
    },
)

# All tool calls made through this LLM instance are policy-enforced.
result = llm.invoke("Process the pending record.")
```

## Session ID Discipline

Generate the `session_id` once at the start of each agent run — not per LLM call. The
session ID is the scope of TrueBearing's sequence memory. If you generate a new UUID on
every LangChain invocation, each call sees an empty history and `only_after` guards will
never trigger, even when the sequence has been violated.
