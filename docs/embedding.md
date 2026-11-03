# Embedding backends

Search compares vectors (embeddings). The `embedding.backend` setting decides where they come from. Each instance chooses its own backend; several instances with different backends are independent of each other.

| Backend | Service | Data leaves the server | Operation |
|---|---|---|---|
| `tei` | Hugging Face text-embeddings-inference (default, model bge-m3) | no | own container, 2 to 12 GB RAM |
| `openai` | OpenAI and any service with an OpenAI-compatible interface | yes, to the provider | no own server |
| `bedrock` | Amazon Bedrock (Titan, Cohere) | yes, but within your own AWS account and the chosen region | no own server |

More backends are planned (see below, "Adding a new backend").

## Settings

All backends share these fields in the `embedding` section:

| Field | Meaning |
|---|---|
| `backend` | `tei`, `openai` or `bedrock` |
| `model` | Model name of the service |
| `dimensions` | Length of the vectors, 1 to 2000. **Fixed per database**, see "Dimension and changing the model". |
| `url` | `tei`: address of the server. `openai`: base address of the API, empty = `https://api.openai.com/v1`. `bedrock`: not used. |
| `api_key` | `openai`: key (`"${EMBEDDING_API_KEY}"`). `tei`: only if the server runs with `--api-key`. |
| `query_prefix`, `document_prefix` | Text placed in front of every search query or every section. Only for models that require it, e.g. E5 (`"query: "` and `"passage: "`). Applies to every backend. |
| `timeout` | Maximum duration of a request, default `60s` |
| `options` | Settings that only the chosen backend knows (below). Unknown keys and invalid values are rejected at startup, with a list of the valid keys. |

On errors 429 and 5xx the server repeats the request up to twice with a growing pause. Error messages never contain the embedded text.

## `tei`

```yaml
embedding:
  backend: "tei"
  url: "http://tei-embed:80"
  model: "BAAI/bge-m3"
  dimensions: 1024
  options:
    batch_size: 16      # texts per request, below TEI's --max-client-batch-size (32)
    normalize: true
    truncate: true
```

**Memory requirements of the TEI server** (measured with bge-m3, ONNX backend):

| Start arguments | Cores | RAM |
|---|---|---|
| `--max-batch-tokens 8192` (default of the Compose file) | 8 | about 12 GB |
| `--max-batch-tokens 1024 --auto-truncate` | 8 | about 3.0 GB |
| `--max-batch-tokens 1024 --auto-truncate` | 2 | about 1.7 GB |

For values smaller than 8192, TEI requires `--auto-truncate`. Atrium's sections are at most 800 tokens long (`chunking.max_tokens`), so nothing gets cut off. On 2 cores a section of about 500 tokens takes roughly 2.5 s, a search query roughly 0.15 s. For small servers, `.env` sets the arguments:

```
TEI_ARGS=--model-id BAAI/bge-m3 --max-batch-tokens 1024 --auto-truncate
```

For a shared server there is an example with limits for CPU and memory: [deploy/examples/docker-compose.limits.yml](../deploy/examples/docker-compose.limits.yml). How to use it is described at the top of the file.

The TEI image only exists for x86 (amd64), so ARM servers (e.g. AWS Graviton, Hetzner CAX) are ruled out. On first start TEI downloads the model (about 2 GB) from Hugging Face; in air-gapped networks the model cache must be copied into the volume beforehand.

## `openai`

```yaml
embedding:
  backend: "openai"
  model: "text-embedding-3-small"
  dimensions: 1536
  api_key: "${EMBEDDING_API_KEY}"
  options:
    send_dimensions: true   # for text-embedding-3-*, so the service shortens to `dimensions`
    batch_size: 32
    # organization: "org-..."
```

- **`text-embedding-3-large`** has 3072 dimensions, the index allows at most 2000. With `send_dimensions: true` and e.g. `dimensions: 1024` or `1536` the service shortens the vectors.
- **Other services:** `url` selects the provider if it offers the OpenAI interface (`POST {url}/embeddings`). According to their documentation this applies e.g. to Mistral, IONOS AI Model Hub, OVHcloud AI Endpoints, Ollama (`http://host:11434/v1`), vLLM, LiteLLM and TEI itself (`/v1/embeddings`). We have not tried all of them. With a custom endpoint the key is optional. Only set `send_dimensions` if the service knows the `dimensions` parameter.
- Azure OpenAI uses different addresses and headers and is not supported.

## `bedrock`

```yaml
embedding:
  backend: "bedrock"
  model: "amazon.titan-embed-text-v2:0"
  dimensions: 1024
  options:
    region: "eu-central-1"      # or environment variable AWS_REGION
    # endpoint: "https://vpce-....bedrock-runtime.eu-central-1.vpce.amazonaws.com"
    # concurrency: 4            # Titan sends one text per request
    # batch_size: 32            # Cohere, at most 96
```

| Model | Dimensions | Input |
|---|---|---|
| `amazon.titan-embed-text-v2:0` | 256, 512, 1024 | up to 8192 tokens |
| `cohere.embed-multilingual-v3`, `cohere.embed-english-v3` | 1024 | **512 tokens**, longer texts are cut off at the end |
| `cohere.embed-v4:0` | 256, 512, 1024, 1536 | long |

Inference profiles (`eu.cohere.embed-v4:0`) are recognized as well. With Cohere v3, `chunking.max_tokens` should be at most 500.

- **Credentials** come from the usual AWS chain: IAM role of the container (ECS, EC2), environment variables or profile. No key appears in the configuration. With Docker Compose, the Compose file passes `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and `AWS_SESSION_TOKEN` through from `.env`.
- **Permission:** The role only needs to be allowed to invoke the model:

  ```json
  {
    "Version": "2012-10-17",
    "Statement": [{
      "Effect": "Allow",
      "Action": "bedrock:InvokeModel",
      "Resource": "arn:aws:bedrock:eu-central-1::foundation-model/amazon.titan-embed-text-v2:0"
    }]
  }
  ```

  With an inference profile, its ARN must be allowed in addition. Model access must be enabled in the Bedrock console of the region.
- **Without internet:** `endpoint` can point to a VPC endpoint (PrivateLink).
- **Status:** This backend is tested with a fake of the AWS client, not against a real AWS account. Before productive use, check it once with your own account: start the server, call `/readyz`, store an entry and search for it.

## Dimension and changing the model

The vector column is created with `embedding.dimensions` on first start. After that:
- The server refuses to start with a **different dimension**.
- With the **same dimension but a different model** it only warns. Vectors of different models are not comparable, so search then returns poor results.
- A job that re-embeds all entries is **not implemented yet**. The error message mentions `admin_reembed`, which does not exist yet. Until then: decide on provider and model before the first entry is stored. Switching between providers of the same model (e.g. bge-m3 locally and bge-m3 at a cloud service), on the other hand, works without re-embedding.

## Data protection

With `openai` and `bedrock`, the text of **every** entry goes to the provider, including entries from personal spaces. This belongs in the data protection documentation: data processing agreement, processing location (with Bedrock the chosen region in your own account), retention. Only `tei` keeps all data in-house.

## Readiness and troubleshooting

`/readyz` and `admin_health` call the embedder. With `openai` and `bedrock` the ping embeds one word and checks the dimension, at most once per minute, so that checks hardly cause any cost. Typical messages:

| Message | Cause |
|---|---|
| "check credentials" (status 401 or 403) | Key wrong or without permission |
| "bedrock denies access" | IAM permission or model access in the region is missing |
| "throttles" | Rate limit of the service |
| "returns different dimensions" | `dimensions` does not match the model |
| "aws config", "no EC2 IMDS role" | no AWS credentials found |

## Adding a new backend

A backend is a file in `internal/embed/`:

1. Create a type that satisfies the `backend` interface: `embed(ctx, texts, purpose)` and `ping(ctx)`. Prefixes, the check of the number and dimension of the vectors, and the case without texts are handled by the shared part.
2. Write a factory: `func(Settings, *options, *http.Client) (backend, error)`. It must not access the network, reads its settings via `o.str`, `o.integer` and `o.boolean`, reports errors with `o.fail` and finally calls `o.done()`.
3. Register it in `init()` with `register("name", factory)`.
4. Write tests with a fake server or fake client (templates: `openai_test.go`, `bedrock_test.go`).

After that the name appears in `embed.Backends()` and in the configuration check, without further changes. Anyone who needs to distinguish between a search query and a document (`input_type`) reads the `purpose` parameter.

Examples for later backends: Azure OpenAI, Cohere and Voyage AI directly, Google Vertex AI.
