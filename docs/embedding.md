# Embedding-Backends

Die Suche vergleicht Vektoren (Einbettungen). Woher sie kommen, legt `embedding.backend` fest. Jede Instanz wählt ihr Backend selbst; auch mehrere Instanzen mit verschiedenen Backends sind unabhängig voneinander.

| Backend | Dienst | Daten verlassen den Server | Betrieb |
|---|---|---|---|
| `tei` | Hugging Face text-embeddings-inference (Standard, Modell bge-m3) | nein | eigener Container, 2 bis 12 GB RAM |
| `openai` | OpenAI und jeder Dienst mit OpenAI-kompatibler Schnittstelle | ja, an den Anbieter | kein eigener Server |
| `bedrock` | Amazon Bedrock (Titan, Cohere) | ja, aber im eigenen AWS-Konto und in der gewählten Region | kein eigener Server |

Weitere Backends sind vorgesehen (siehe unten, „Ein neues Backend hinzufügen“).

## Einstellungen

Alle Backends teilen sich diese Felder im Abschnitt `embedding`:

| Feld | Bedeutung |
|---|---|
| `backend` | `tei`, `openai` oder `bedrock` |
| `model` | Modellname des Dienstes |
| `dimensions` | Länge der Vektoren, 1 bis 2000. **Pro Datenbank fest**, siehe „Dimension und Modellwechsel“. |
| `url` | `tei`: Adresse des Servers. `openai`: Basisadresse der API, leer = `https://api.openai.com/v1`. `bedrock`: nicht verwendet. |
| `api_key` | `openai`: Schlüssel (`"${EMBEDDING_API_KEY}"`). `tei`: nur wenn der Server mit `--api-key` läuft. |
| `query_prefix`, `document_prefix` | Text, der vor jede Suchfrage bzw. jeden Abschnitt gesetzt wird. Nur für Modelle, die das verlangen, z. B. E5 (`"query: "` und `"passage: "`). Gilt für jedes Backend. |
| `timeout` | Höchstdauer einer Anfrage, Standard `60s` |
| `options` | Einstellungen, die nur das gewählte Backend kennt (unten). Unbekannte Schlüssel und ungültige Werte werden beim Start abgelehnt, mit Liste der gültigen Schlüssel. |

Bei Fehlern 429 und 5xx wiederholt der Server die Anfrage bis zu zweimal mit wachsender Pause. Fehlermeldungen enthalten nie den eingebetteten Text.

## `tei`

```yaml
embedding:
  backend: "tei"
  url: "http://tei-embed:80"
  model: "BAAI/bge-m3"
  dimensions: 1024
  options:
    batch_size: 16      # Texte je Anfrage, unter dem --max-client-batch-size von TEI (32)
    normalize: true
    truncate: true
```

**Speicherbedarf des TEI-Servers** (gemessen mit bge-m3, ONNX-Backend):

| Startargumente | Kerne | RAM |
|---|---|---|
| `--max-batch-tokens 8192` (Standard der Compose-Datei) | 8 | rund 12 GB |
| `--max-batch-tokens 1024 --auto-truncate` | 8 | rund 3,0 GB |
| `--max-batch-tokens 1024 --auto-truncate` | 2 | rund 1,7 GB |

TEI verlangt für kleinere Werte als 8192 zwingend `--auto-truncate`. Die Abschnitte von Atrium sind höchstens 800 Tokens lang (`chunking.max_tokens`), es wird also nichts abgeschnitten. Auf 2 Kernen dauert ein Abschnitt von rund 500 Tokens etwa 2,5 s, eine Suchanfrage etwa 0,15 s. Für kleine Server stellt `.env` die Argumente ein:

```
TEI_ARGS=--model-id BAAI/bge-m3 --max-batch-tokens 1024 --auto-truncate
```

Für einen geteilten Server gibt es ein Beispiel mit Limits für CPU und Speicher: [deploy/examples/docker-compose.limits.yml](../deploy/examples/docker-compose.limits.yml). Die Verwendung steht im Kopf der Datei.

Das TEI-Image gibt es nur für x86 (amd64), ARM-Server (z. B. AWS Graviton, Hetzner CAX) scheiden dafür aus. Beim ersten Start lädt TEI das Modell (rund 2 GB) von Hugging Face; in abgeschotteten Netzen muss der Modell-Cache vorher in das Volume kopiert werden.

## `openai`

```yaml
embedding:
  backend: "openai"
  model: "text-embedding-3-small"
  dimensions: 1536
  api_key: "${EMBEDDING_API_KEY}"
  options:
    send_dimensions: true   # bei text-embedding-3-*, damit der Dienst auf `dimensions` kürzt
    batch_size: 32
    # organization: "org-..."
```

- **`text-embedding-3-large`** hat 3072 Dimensionen, der Index erlaubt höchstens 2000. Mit `send_dimensions: true` und z. B. `dimensions: 1024` oder `1536` kürzt der Dienst die Vektoren.
- **Andere Dienste:** `url` wählt den Anbieter, wenn er die OpenAI-Schnittstelle (`POST {url}/embeddings`) anbietet. Laut deren Dokumentation gilt das z. B. für Mistral, IONOS AI Model Hub, OVHcloud AI Endpoints, Ollama (`http://host:11434/v1`), vLLM, LiteLLM und TEI selbst (`/v1/embeddings`). Wir haben nicht alle ausprobiert. Mit eigenem Endpunkt ist der Schlüssel optional. `send_dimensions` nur setzen, wenn der Dienst den Parameter `dimensions` kennt.
- Azure OpenAI nutzt andere Adressen und Kopfzeilen und wird nicht unterstützt.

## `bedrock`

```yaml
embedding:
  backend: "bedrock"
  model: "amazon.titan-embed-text-v2:0"
  dimensions: 1024
  options:
    region: "eu-central-1"      # oder Umgebungsvariable AWS_REGION
    # endpoint: "https://vpce-....bedrock-runtime.eu-central-1.vpce.amazonaws.com"
    # concurrency: 4            # Titan sendet einen Text je Anfrage
    # batch_size: 32            # Cohere, höchstens 96
```

| Modell | Dimensionen | Eingabe |
|---|---|---|
| `amazon.titan-embed-text-v2:0` | 256, 512, 1024 | bis 8192 Tokens |
| `cohere.embed-multilingual-v3`, `cohere.embed-english-v3` | 1024 | **512 Tokens**, längere Texte werden am Ende abgeschnitten |
| `cohere.embed-v4:0` | 256, 512, 1024, 1536 | lang |

Auch Inferenzprofile (`eu.cohere.embed-v4:0`) werden erkannt. Bei Cohere v3 sollte `chunking.max_tokens` höchstens 500 betragen.

- **Zugangsdaten** kommen aus der üblichen AWS-Kette: IAM-Rolle des Containers (ECS, EC2), Umgebungsvariablen oder Profil. In der Konfiguration steht kein Schlüssel. Bei Docker Compose reicht die Compose-Datei `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` und `AWS_SESSION_TOKEN` aus `.env` durch.
- **Berechtigung:** Die Rolle braucht nur das Aufrufen des Modells:

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

  Bei einem Inferenzprofil muss zusätzlich dessen ARN freigegeben sein. Der Modellzugriff muss in der Bedrock-Konsole der Region aktiviert sein.
- **Ohne Internet:** `endpoint` kann auf einen VPC-Endpunkt (PrivateLink) zeigen.
- **Stand:** Dieses Backend ist mit einem Fake des AWS-Clients getestet, nicht gegen ein echtes AWS-Konto. Vor dem produktiven Einsatz einmal mit eigenem Konto prüfen: Server starten, `/readyz` aufrufen, einen Eintrag speichern und suchen.

## Dimension und Modellwechsel

Die Spalte der Vektoren wird beim ersten Start mit `embedding.dimensions` angelegt. Danach gilt:
- Eine **andere Dimension** verweigert der Server beim Start.
- Bei **gleicher Dimension, aber anderem Modell** warnt er nur. Vektoren verschiedener Modelle sind nicht vergleichbar, die Suche liefert dann schlechte Treffer.
- Ein Job, der alle Einträge neu einbettet, ist **noch nicht umgesetzt**. Die Fehlermeldung nennt `admin_reembed`, das es noch nicht gibt. Bis dahin gilt: Anbieter und Modell vor dem ersten Speichern festlegen. Zwischen Anbietern desselben Modells (z. B. bge-m3 lokal und bge-m3 bei einem Cloud-Dienst) kann man dagegen wechseln, ohne neu einzubetten.

## Datenschutz

Bei `openai` und `bedrock` geht der Text **jedes** Eintrags an den Anbieter, auch aus persönlichen Bereichen. Das gehört in die Datenschutzunterlagen: Auftragsverarbeitung, Standort der Verarbeitung (bei Bedrock die gewählte Region im eigenen Konto), Aufbewahrung. Nur `tei` hält alle Daten im eigenen Haus.

## Bereitschaft und Fehlersuche

`/readyz` und `admin_health` rufen den Embedder auf. Bei `openai` und `bedrock` bettet der Ping ein Wort ein und prüft die Dimension, höchstens einmal pro Minute, damit Prüfungen kaum Kosten erzeugen. Typische Meldungen:

| Meldung | Ursache |
|---|---|
| „check credentials“ (Status 401 oder 403) | Schlüssel falsch oder ohne Recht |
| „bedrock denies access“ | IAM-Recht oder Modellzugriff in der Region fehlt |
| „throttles“ | Ratenlimit des Dienstes |
| „returns different dimensions“ | `dimensions` passt nicht zum Modell |
| „aws config“, „no EC2 IMDS role“ | keine AWS-Zugangsdaten gefunden |

## Ein neues Backend hinzufügen

Ein Backend ist eine Datei in `internal/embed/`:

1. Typ anlegen, der das Interface `backend` erfüllt: `embed(ctx, texts, purpose)` und `ping(ctx)`. Präfixe, die Prüfung von Anzahl und Dimension der Vektoren und der Fall ohne Texte übernimmt der gemeinsame Teil.
2. Eine Fabrik schreiben: `func(Settings, *options, *http.Client) (backend, error)`. Sie darf keine Netzwerkzugriffe machen, liest ihre Einstellungen über `o.str`, `o.integer` und `o.boolean`, meldet Fehler mit `o.fail` und ruft am Ende `o.done()`.
3. In `init()` mit `register("name", fabrik)` anmelden.
4. Tests mit einem Fake-Server oder Fake-Client schreiben (Vorlagen: `openai_test.go`, `bedrock_test.go`).

Danach erscheint der Name in `embed.Backends()` und in der Prüfung der Konfiguration, ohne weitere Änderungen. Wer zwischen Suchfrage und Dokument unterscheiden muss (`input_type`), liest dazu den Parameter `purpose`.

Beispiele für spätere Backends: Azure OpenAI, Cohere und Voyage AI direkt, Google Vertex AI.
