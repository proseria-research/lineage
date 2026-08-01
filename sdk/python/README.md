# Lineage Python SDK

The standard-library Python client for Lineage's `/v1` Model API.

```python
from lineage import Client

lin = Client("http://localhost:8081", actor="training-ci")
lin.publish(
    model="fraud-detector",
    version="1.4.0",
    artifacts=[lin.model_file("model.onnx", format=("onnx", "1.16"))],
    lineage=[("trained_on", "s3://datasets/fraud-q2")],
)
lin.transition("fraud-detector", "1.4.0", to="staging")
paths = lin.download("fraud-detector", stage="staging", dest="./model")
```

`Client` uses only the Python standard library. It handles all three upload modes (signed
direct PUT, multipart, and stream-through), streams file bodies instead of buffering them,
verifies SHA-256 digests on upload and download, and sends `X-Lineage-Actor` when configured.

`download` fetches every `MODEL` artifact of the resolution and returns the paths written — a
model is usually several files (sharded weights, config, tokenizer). `DOC` artifacts such as
model cards are excluded, matching the `lineage://` KServe initializer. Pass
`artifact="model.onnx"` for a single file, or `kind=None` to fetch everything.

Request URLs are built from `lineage/_openapi.py`, generated from the server's OpenAPI
document by `sdk/generate.py` (`make sdk-check`).
