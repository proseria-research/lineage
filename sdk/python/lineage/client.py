"""Thin ergonomic layer over the Lineage OpenAPI /v1 contract.

The transport deliberately uses urllib so publishing and pulling work in minimal training and
serving images without adding an HTTP dependency.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from hashlib import sha256
import json
import mimetypes
import os
from pathlib import Path
import re
import subprocess
from typing import Any, Iterable, Literal
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode
from urllib.request import Request, urlopen

from ._openapi import API_VERSION, PATHS

__api_version__ = API_VERSION


class APIError(RuntimeError):
    """A problem response returned by the Lineage Model API."""

    def __init__(self, status: int, code: str, detail: str, details: dict[str, Any] | None = None):
        self.status, self.code, self.detail, self.details = status, code, detail, details or {}
        super().__init__(f"{status} {code}: {detail}")


@dataclass(frozen=True)
class Artifact:
    """An artifact supplied to :meth:`Client.publish`."""

    name: str
    path: Path | None = None
    uri: str | None = None
    kind: Literal["MODEL", "DOC"] = "MODEL"
    model_format: tuple[str, str | None] | None = None
    media_type: str | None = None
    storage_backend: str | None = None
    service_account: str | None = None

    def __post_init__(self) -> None:
        if (self.path is None) == (self.uri is None):
            raise ValueError("an artifact needs exactly one of path or uri")


@dataclass(frozen=True)
class Resolution:
    """The consumer-facing response from ``GET /resolve``."""

    model: str
    version: str
    stage: str
    digest: str | None
    model_format: dict[str, Any] | None
    artifacts: list[dict[str, Any]]
    raw: dict[str, Any] = field(repr=False)

    @property
    def storage_uri(self) -> str | None:
        return self.artifacts[0].get("storageUri") if self.artifacts else None

    @property
    def signed_url(self) -> str | None:
        return self.artifacts[0].get("signedUrl") if self.artifacts else None


class Client:
    """Lineage `/v1` client.

    ``base_url`` is the Model API origin (for example ``https://lineage.internal``). The SDK
    never performs authentication; ``actor`` is optional audit attribution forwarded as the
    trusted ``X-Lineage-Actor`` header.
    """

    def __init__(self, base_url: str, *, actor: str | None = None, timeout: float = 60.0):
        self.base_url = base_url.rstrip("/")
        self.actor = actor
        self.timeout = timeout

    @staticmethod
    def _path(operation: str, **params: str) -> str:
        """Build a request path from the generated OpenAPI manifest.

        Every URL the client issues is resolved through PATHS, so an operation or path that
        moves in the contract fails at generation time instead of becoming a runtime 404.
        """
        _, template = PATHS[operation]
        return re.sub(r"\{(\w+)\}", lambda m: quote(params[m.group(1)], safe=""), template)

    def model_file(
        self,
        path: str | os.PathLike[str],
        *,
        name: str | None = None,
        format: tuple[str, str | None] | None = None,
        media_type: str | None = None,
    ) -> Artifact:
        file_path = Path(path)
        return Artifact(
            name=name or file_path.name,
            path=file_path,
            model_format=format,
            media_type=media_type or mimetypes.guess_type(file_path.name)[0] or "application/octet-stream",
        )

    def artifact_uri(
        self,
        uri: str,
        *,
        name: str,
        kind: Literal["MODEL", "DOC"] = "MODEL",
        format: tuple[str, str | None] | None = None,
        storage_backend: str | None = None,
    ) -> Artifact:
        return Artifact(name=name, uri=uri, kind=kind, model_format=format, storage_backend=storage_backend)

    def publish(
        self,
        *,
        model: str,
        version: str,
        artifacts: Iterable[Artifact] = (),
        lineage: Iterable[tuple[str, str]] = (),
        description: str | None = None,
        labels: dict[str, str] | None = None,
        auto_capture: bool = True,
        idempotency_key: str | None = None,
    ) -> dict[str, Any]:
        """Publish a version, upload local artifacts, and add optional lineage edges.

        URI artifacts are registered inline. Local files use the API's initiate/upload/finalize
        flow, preserving direct-to-storage uploads where the backend supports signed URLs.

        The returned dict carries the created version under ``version`` and every artifact —
        inline and uploaded — under ``artifacts``.

        ``idempotency_key`` defaults to one derived from the model and version, so a retried
        publish replays the original response instead of creating a second version. The server
        keys these globally, hence the namespaced default.
        """
        artifacts = list(artifacts)
        inline = [self._artifact_input(a) for a in artifacts if a.uri]
        self._ensure_model(model)
        body: dict[str, Any] = {"name": version}
        if description is not None:
            body["description"] = description
        if self.actor:
            body["author"] = self.actor
        if labels:
            body["labels"] = labels
        if inline:
            body["artifacts"] = inline
        key = idempotency_key or f"version:{model}@{version}"
        result = self._json("POST", self._path("publishVersion", model=model), body, idempotency_key=key)
        # The version-create response only knows about inline artifacts; uploads happen after it,
        # so their finalize responses are merged in to make the return value complete.
        uploaded = [self._upload(model, version, a) for a in artifacts if a.path]
        if uploaded:
            result["artifacts"] = list(result.get("artifacts") or []) + uploaded
        edges = list(lineage)
        if auto_capture:
            edges.extend(self._captured_lineage())
        for relation, target in edges:
            to = {"uri": target} if "://" in target else {"version": target}
            self._json("POST", self._path("addLineage", model=model, version=version), {"relation": relation, "to": to})
        return result

    def transition(self, model: str, version: str, *, to: str, reason: str | None = None) -> dict[str, Any]:
        body: dict[str, Any] = {"to": to}
        if reason:
            body["reason"] = reason
        return self._json("POST", self._path("transitionVersion", model=model, version=version), body)

    def resolve(self, model: str, *, stage: str | None = None, version: str | None = None) -> Resolution:
        if stage and version:
            raise ValueError("stage and version are mutually exclusive")
        query = urlencode({k: v for k, v in {"stage": stage, "version": version}.items() if v})
        path = self._path("resolve", model=model)
        raw = self._json("GET", path + (f"?{query}" if query else ""))
        return Resolution(raw["model"], raw["version"], raw["stage"], raw.get("digest"), raw.get("modelFormat"), raw.get("artifacts", []), raw)

    def download(
        self,
        model: str,
        *,
        stage: str | None = None,
        version: str | None = None,
        dest: str | os.PathLike[str],
        artifact: str | None = None,
        kind: str | None = "MODEL",
    ) -> list[Path]:
        """Download a resolution's artifacts into ``dest`` and return the files written.

        Every artifact of ``kind`` is fetched — sharded weights, config and tokenizer are
        separate artifacts of one version, so taking only the first would silently produce an
        unusable model directory. ``kind`` defaults to ``MODEL`` so model cards and other DOC
        artifacts stay out of a serving directory; pass ``kind=None`` for all of them. Naming
        an ``artifact`` selects that one regardless of kind. Each file's digest is verified
        when the resolution declares one.
        """
        resolution = self.resolve(model, stage=stage, version=version)
        if artifact is not None:
            selected = [a for a in resolution.artifacts if a.get("name") == artifact]
        else:
            selected = [a for a in resolution.artifacts if kind is None or a.get("kind") == kind]
        if not selected:
            if artifact:
                detail = f"resolution has no artifact named '{artifact}'"
            elif kind:
                detail = f"resolution contains no {kind} artifacts"
            else:
                detail = "resolution contains no artifacts"
            raise APIError(409, "failed_precondition", detail)
        target = Path(dest)
        target.mkdir(parents=True, exist_ok=True)
        written: list[Path] = []
        for item in selected:
            name = item["name"]
            # The name comes from the server, so it is checked before being joined into a
            # local path: a bad or compromised registry must not be able to write outside dest.
            if name in (".", "..") or "/" in name or "\\" in name or Path(name).is_absolute():
                raise APIError(422, "unprocessable", f"refusing unsafe artifact name '{name}'")
            output = target / name
            url = item.get("signedUrl") or self.base_url + self._path(
                "fetchContent", model=model, version=resolution.version, artifact=name
            )
            self._download(url, output)
            expected = item.get("digest")
            if expected and self._digest(output) != expected:
                output.unlink(missing_ok=True)
                raise APIError(422, "unprocessable", f"digest mismatch while downloading {name}")
            written.append(output)
        return written

    def lineage(self, model: str, version: str, *, direction: str = "upstream", depth: int = 3) -> dict[str, Any]:
        path = self._path("listLineage", model=model, version=version)
        return self._json("GET", path + "?" + urlencode({"direction": direction, "depth": depth}))

    def _ensure_model(self, model: str) -> None:
        try:
            self._json("POST", self._path("createModel"), {"name": model}, idempotency_key=f"model:{model}")
        except APIError as err:
            if err.code != "already_exists":
                raise

    def _upload(self, model: str, version: str, artifact: Artifact) -> dict[str, Any]:
        assert artifact.path is not None
        if not artifact.path.is_file():
            raise FileNotFoundError(artifact.path)
        size = artifact.path.stat().st_size
        body = self._artifact_input(artifact, size_bytes=size)
        body.pop("uri", None)
        ticket = self._json("POST", self._path("initiateUpload", model=model, version=version), body)
        digest = self._digest(artifact.path)
        finalize = self._path("finalizeUpload", model=model, version=version)
        if ticket.get("multipart"):
            parts = self._upload_parts(artifact.path, ticket)
            return self._json("POST", finalize, {"uploadId": ticket["uploadId"], "digest": digest, "parts": parts})
        url = ticket.get("contentUrl") or ticket.get("url")
        if not url:
            raise APIError(500, "internal", "upload ticket had no upload URL")
        self._put_file(
            self._absolute(url), artifact.path, ticket.get("method", "PUT"),
            ticket.get("headers", {}), artifact.media_type,
        )
        return self._json("POST", finalize, {"uploadId": ticket["uploadId"], "digest": digest})

    def _upload_parts(self, path: Path, ticket: dict[str, Any]) -> list[dict[str, Any]]:
        """Upload presigned multipart parts in order and return their storage ETags."""
        completed: list[dict[str, Any]] = []
        with path.open("rb") as source:
            for part in ticket.get("parts", []):
                data = source.read(int(ticket["partSize"]))
                if not data:
                    break
                etag = self._put_bytes(part["url"], data, part.get("headers", {}))
                if not etag:
                    raise APIError(502, "bad_gateway", f"multipart part {part['partNumber']} returned no ETag")
                completed.append({"partNumber": part["partNumber"], "etag": etag})
        if len(completed) != len(ticket.get("parts", [])):
            raise APIError(500, "internal", "multipart upload did not consume every planned part")
        return completed

    def _artifact_input(self, artifact: Artifact, *, size_bytes: int | None = None) -> dict[str, Any]:
        out: dict[str, Any] = {"name": artifact.name, "kind": artifact.kind}
        if artifact.uri:
            out["uri"] = artifact.uri
        if size_bytes is not None:
            out["sizeBytes"] = size_bytes
        if artifact.model_format:
            out["modelFormat"] = {"name": artifact.model_format[0], **({"version": artifact.model_format[1]} if artifact.model_format[1] else {})}
        if artifact.media_type:
            out["mediaType"] = artifact.media_type
        if artifact.storage_backend:
            out["storageBackend"] = artifact.storage_backend
        if artifact.service_account:
            out["serviceAccount"] = artifact.service_account
        return out

    def _captured_lineage(self) -> list[tuple[str, str]]:
        edges: list[tuple[str, str]] = []
        if run := os.getenv("LINEAGE_RUN_URI") or os.getenv("LINEAGE_RUN_ID"):
            if "://" not in run:
                run = "run://" + run
            edges.append(("produced_by", run))
        elif sha := self._git_sha():
            edges.append(("produced_by", "git://" + sha))
        if parent := os.getenv("LINEAGE_DERIVED_FROM"):
            edges.extend(("derived_from", item.strip()) for item in parent.split(",") if item.strip())
        return edges

    @staticmethod
    def _git_sha() -> str | None:
        try:
            return subprocess.check_output(["git", "rev-parse", "HEAD"], stderr=subprocess.DEVNULL, text=True, timeout=1).strip() or None
        except (OSError, subprocess.SubprocessError):
            return None

    def _absolute(self, url: str) -> str:
        """Resolve an upload/download target against the API origin.

        Stream-through tickets carry a server-relative ``contentUrl``; signed tickets carry an
        absolute URL on the storage backend. Only the former needs the origin prepended.
        """
        return self.base_url + url if url.startswith("/") else url

    def _json(self, method: str, path: str, body: dict[str, Any] | None = None, *, idempotency_key: str | None = None) -> dict[str, Any]:
        headers = {"Accept": "application/json"}
        if self.actor:
            headers["X-Lineage-Actor"] = self.actor
        if idempotency_key:
            headers["Idempotency-Key"] = idempotency_key
        data = None if body is None else json.dumps(body).encode()
        if data is not None:
            headers["Content-Type"] = "application/json"
        request = Request(self.base_url + path, data=data, headers=headers, method=method)
        try:
            with urlopen(request, timeout=self.timeout) as response:
                payload = response.read()
                return json.loads(payload) if payload else {}
        except HTTPError as error:
            self._raise_api_error(error)
        except URLError as error:
            raise APIError(0, "network_error", str(error.reason)) from error
        raise AssertionError("unreachable")

    def _put_file(self, url: str, path: Path, method: str, headers: dict[str, str], media_type: str | None = None) -> None:
        """PUT a file without buffering it.

        The body is the open file object, so http.client streams it in blocks — model weights
        are routinely larger than memory. Content-Length is set explicitly because urllib
        otherwise falls back to chunked encoding, which signed object-store PUTs reject.
        """
        request_headers = dict(headers)
        if self.actor and url.startswith(self.base_url):
            request_headers.setdefault("X-Lineage-Actor", self.actor)
        request_headers.setdefault("Content-Length", str(path.stat().st_size))
        request_headers.setdefault("Content-Type", media_type or "application/octet-stream")
        with path.open("rb") as source:
            request = Request(url, data=source, headers=request_headers, method=method)
            try:
                with urlopen(request, timeout=self.timeout):
                    pass
            except HTTPError as error:
                self._raise_api_error(error)

    def _put_bytes(self, url: str, data: bytes, headers: dict[str, str]) -> str | None:
        request = Request(url, data=data, headers=headers, method="PUT")
        try:
            with urlopen(request, timeout=self.timeout) as response:
                return response.headers.get("ETag", "").strip('"') or None
        except HTTPError as error:
            self._raise_api_error(error)
        raise AssertionError("unreachable")

    def _download(self, url: str, output: Path) -> None:
        request = Request(url, headers={"X-Lineage-Actor": self.actor} if self.actor and url.startswith(self.base_url) else {})
        try:
            with urlopen(request, timeout=self.timeout) as response, output.open("wb") as destination:
                while chunk := response.read(1024 * 1024):
                    destination.write(chunk)
        except HTTPError as error:
            self._raise_api_error(error)

    @staticmethod
    def _digest(path: Path) -> str:
        digest = sha256()
        with path.open("rb") as source:
            while chunk := source.read(1024 * 1024):
                digest.update(chunk)
        return "sha256:" + digest.hexdigest()

    @staticmethod
    def _raise_api_error(error: HTTPError) -> None:
        try:
            problem = json.loads(error.read())
        except (json.JSONDecodeError, OSError):
            problem = {}
        raise APIError(error.code, problem.get("code", "http_error"), problem.get("detail", error.reason), problem.get("details")) from error
