# Using TrueBearing with OpenAI Clients

TrueBearing exposes a standard HTTP MCP endpoint. Any OpenAI client that supports a
`base_url` override can route tool calls through it without code changes beyond the
client constructor.

> **Note:** The Python SDK (`from truebearing import PolicyProxy`) handles the `base_url`
> injection automatically for Anthropic clients. For OpenAI clients, set `base_url`
> manually as shown below until first-class OpenAI SDK support ships.

## Prerequisites

Complete the [Quick Start](../../README.md#quick-start-confirmed-blocked-call-in-under-10-minutes)
steps first:

1. `truebearing agent register my-agent --policy ./truebearing.policy.yaml`
2. `truebearing serve --upstream <mcp-url> --policy ./truebearing.policy.yaml`
3. Export the agent JWT: `export TRUEBEARING_AGENT_JWT=$(cat ~/.truebearing/keys/my-agent.jwt)`

## Python (`openai` package)

```python
import os
import uuid
from openai import OpenAI

session_id = str(uuid.uuid4())

client = OpenAI(
    base_url="http://localhost:7773/mcp/v1",
    default_headers={
        "Authorization": f"Bearer {os.environ['TRUEBEARING_AGENT_JWT']}",
        "X-TrueBearing-Session-ID": session_id,
    },
)

# All tool calls now flow through TrueBearing. Policy is enforced transparently.
response = client.chat.completions.create(
    model="gpt-4o",
    messages=[{"role": "user", "content": "Process the pending record."}],
)
```

## Node.js (`openai` npm package)

```typescript
import OpenAI from 'openai';
import { randomUUID } from 'crypto';

const sessionId = randomUUID();

const client = new OpenAI({
  baseURL: 'http://localhost:7773/mcp/v1',
  defaultHeaders: {
    Authorization: `Bearer ${process.env.TRUEBEARING_AGENT_JWT}`,
    'X-TrueBearing-Session-ID': sessionId,
  },
});

// All tool calls now flow through TrueBearing. Policy is enforced transparently.
const response = await client.chat.completions.create({
  model: 'gpt-4o',
  messages: [{ role: 'user', content: 'Process the pending record.' }],
});
```

## How It Works

TrueBearing evaluates every `tools/call` JSON-RPC request against the policy before
forwarding it upstream. Denied calls receive a structured JSON-RPC error; allowed calls
are forwarded and the response is streamed back unchanged. The `base_url` redirect is
the only change required in the client.

**Session ID discipline:** Use a stable UUID per agent run (not per request). The session
ID is the unit of history for sequence predicates — reusing a session ID across runs will
merge their histories. Generate once at agent startup, not per tool call.
