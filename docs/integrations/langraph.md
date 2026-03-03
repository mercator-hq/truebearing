# Using TrueBearing with LangGraph

LangGraph agents use LangChain LLM instances for their model nodes. Set `base_url` and
the two TrueBearing headers on the LLM before passing it to your graph. The graph itself
requires no changes.

## Prerequisites

Complete the [Quick Start](../../README.md#quick-start-confirmed-blocked-call-in-under-10-minutes)
steps first:

1. `truebearing agent register my-agent --policy ./truebearing.policy.yaml`
2. `truebearing serve --upstream <mcp-url> --policy ./truebearing.policy.yaml`
3. Export the agent JWT: `export TRUEBEARING_AGENT_JWT=$(cat ~/.truebearing/keys/my-agent.jwt)`

## Example

```python
import os
import uuid
from langchain_openai import ChatOpenAI
from langgraph.prebuilt import create_react_agent

session_id = str(uuid.uuid4())

# Configure the model to route through TrueBearing.
model = ChatOpenAI(
    model="gpt-4o",
    openai_api_base="http://localhost:7773/mcp/v1",
    default_headers={
        "Authorization": f"Bearer {os.environ['TRUEBEARING_AGENT_JWT']}",
        "X-TrueBearing-Session-ID": session_id,
    },
)

# Define tools as normal — TrueBearing intercepts at the HTTP layer.
tools = [read_record_tool, validate_record_tool, submit_record_tool]

graph = create_react_agent(model, tools=tools)

# Run the agent. Every tool call is evaluated against the policy.
result = graph.invoke({"messages": [("user", "Process and submit the pending record.")]})
```

## Multi-step Graph Runs and Session Continuity

LangGraph agents often loop over multiple model + tool steps within a single graph
invocation. Use the **same `session_id`** for the entire graph run so TrueBearing sees
the full tool-call sequence in a single session. If you reset the session ID between
steps, sequence predicates (e.g. `only_after: validate_record`) will not fire even when
the constraint has been violated.

## Parallel Node Execution

When a LangGraph graph executes tool nodes in parallel, concurrent calls may arrive at
the proxy simultaneously. TrueBearing evaluates each call against the session history
at the moment it is received. Parallel calls that depend on each other via `only_after`
may race — consider serialising the tool steps in your graph if sequence ordering matters
for those tools.
