"""Build data/words.json + data/related.json from GloVe vectors.

Usage: python scripts/build.py data/glove.6B.50d.txt
Check the GloVe license before shipping derived data.
"""
import json, sys
import numpy as np

N, K = 30000, 8
words, vecs = [], []
with open(sys.argv[1], "rb") as f:
    first = f.readline()
    parts = first.split()
    if len(parts) == 2 and parts[0].isdigit() and parts[1].isdigit():
        dim = int(parts[1])
        for _ in range(N):
            w_bytes = bytearray()
            while True:
                ch = f.read(1)
                if ch == b" " or not ch:
                    break
                if ch != b"\n":
                    w_bytes.extend(ch)
            if not w_bytes:
                break
            raw = f.read(4 * dim)
            w = w_bytes.decode("utf8", errors="ignore")
            if w.isalpha() and w.isascii() and len(w) > 2:
                words.append(w.lower())
                vecs.append(np.frombuffer(raw, dtype=np.float32).copy())
    else:
        f.seek(0)
        for i, raw_line in enumerate(f):
            if i >= N:
                break
            p = raw_line.decode("utf8", errors="ignore").split()
            if p and p[0].isalpha() and p[0].isascii() and len(p[0]) > 2:
                words.append(p[0].lower())
                vecs.append(np.asarray(p[1:], dtype=np.float32))
V = np.stack(vecs)
V /= np.linalg.norm(V, axis=1, keepdims=True)

related = {}
for s in range(0, len(words), 2000):  # chunked to limit memory
    sims = V[s:s + 2000] @ V.T
    top = np.argpartition(-sims, K + 1, axis=1)[:, :K + 1]
    for r, row in enumerate(top):
        w = words[s + r]
        row = sorted(row, key=lambda j: -sims[r, j])
        related[w] = [words[j] for j in row if words[j] != w][:K]

json.dump(words, open("data/words.json", "w"))
json.dump(related, open("data/related.json", "w"))
print(f"{len(words)} words")
