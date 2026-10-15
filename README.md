# word-api

Word suggestion / related-word / hashtag API. Go + Upstash Redis.

## Endpoints
| Method | Path | Notes |
|---|---|---|
| GET | `/suggest?q=trav&n=8` | prefix autocomplete, ranked by frequency |
| GET | `/related?w=travel` | related words |
| GET | `/hashtags?q=trav` | suggest + related as `#tags` |
| POST | `/learn?word=travel` | bump count; header `X-API-Key: $WRITE_KEY` |
| GET | `/health` | liveness |
| GET | `/llms.txt` | LLM-friendly API documentation (`text/plain`) |

## Setup
```bash
cp .env.example .env            # fill REDIS_URL, WRITE_KEY
go mod tidy                     # creates go.sum (needs Go 1.22+)
```

### Load data (once)
```bash
pip install -r scripts/requirements.txt
# download glove.6B.50d.txt or glove.6B.300d into data/ (check license), then:
python scripts/build.py data/glove.6B.300d
REDIS_URL=... python scripts/loader.py
```

### Run
```bash
export $(grep -v '^#' .env | xargs)
go run .
curl 'localhost:8000/hashtags?q=trav'
```

## Deploy (Render / Railway)
Web Service, runtime **Docker**. Env vars: `REDIS_URL`, `WRITE_KEY`,
`ALLOWED_ORIGINS`, optional `RATE_LIMIT`. `PORT` is injected by the platform.
Use the same region for the host and the Upstash database.

Rate limit and cache are per-instance, in memory.
