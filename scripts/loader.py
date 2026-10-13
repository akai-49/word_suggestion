"""Load data/words.json + data/related.json into Redis (run once, locally).

Usage: REDIS_URL=rediss://... python scripts/loader.py
Writes ~6-7 entries per word; lower N or MAXP if your Redis plan is small.
"""
import json, os, redis

MAXP = 10  # max prefix length indexed
r = redis.from_url(os.environ["REDIS_URL"])
words = json.load(open("data/words.json"))
related = json.load(open("data/related.json"))

p, n = r.pipeline(transaction=False), 0
for rank, w in enumerate(words):  # words are frequency-ordered
    freq = len(words) - rank
    for i in range(1, min(len(w), MAXP) + 1):
        p.zadd(f"ac:{w[:i]}", {w: freq})
    n += 1
    if n % 500 == 0:
        p.execute()
p.execute()

items = list(related.items())
for i in range(0, len(items), 1000):
    r.hset("rel", mapping={k: json.dumps(v) for k, v in items[i:i + 1000]})
print("loaded", len(words), "words")
