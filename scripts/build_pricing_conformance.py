"""Regenerate conformance/pricing.json with metergraph-core's Python catalog.

    python3 scripts/build_pricing_conformance.py <metergraph clone> [--check]

Keeps the fixture's queries and recomputes every expected value from the
prices.yaml in the clone, at the clone's last commit to that file. Run it after
`go run ./cmd/build-prices <clone>`, from the same clone, so the fixture and
the bundled catalog name the same commit. With --check it changes nothing and
exits 1 if any expected value differs: use that to confirm the generator
reproduces the committed fixture before regenerating it.

A query is priced as Replay prices it: a "creator/model" id under its creator,
a bare id under the provider-less aliases. A query that says whether its input
count includes cache reads is restated in the row's own convention first.
"""

from __future__ import annotations

import json
import subprocess
import sys
from datetime import datetime
from pathlib import Path

FIXTURE = Path(__file__).resolve().parent.parent / "conformance" / "pricing.json"
CATALOG_PATH = "core/src/metergraph_core/data/prices.yaml"


def main() -> int:
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    check = "--check" in sys.argv[1:]
    if len(args) != 1:
        print(__doc__, file=sys.stderr)
        return 2
    clone = Path(args[0]).resolve()
    sys.path.insert(0, str(clone / "core" / "src"))
    from metergraph_core import catalog as core_catalog  # noqa: E402
    from metergraph_core.loader import load_catalog  # noqa: E402

    loaded = load_catalog(clone / CATALOG_PATH)
    snapshot = loaded.snapshot if hasattr(loaded, "snapshot") else loaded
    commit = subprocess.run(
        ["git", "-C", str(clone), "log", "-1", "--format=%H", "--", CATALOG_PATH],
        check=True, capture_output=True, text=True,
    ).stdout.strip()
    version = _catalog_version(clone / CATALOG_PATH)

    fixture = json.loads(FIXTURE.read_text())
    differ = 0
    for case in fixture["cases"]:
        expected = _price(snapshot, core_catalog, case["query"])
        if expected != case["expected"]:
            differ += 1
            if check:
                print(f"{case['name']}: {json.dumps(case['expected'])} -> {json.dumps(expected)}")
        case["expected"] = expected
    if check:
        print(f"{differ} of {len(fixture['cases'])} cases differ")
        return 1 if differ else 0
    fixture["catalog"] = {"version": version, "commit": commit}
    FIXTURE.write_text(json.dumps(fixture, indent=1, ensure_ascii=False) + "\n")
    print(f"Wrote {len(fixture['cases'])} cases from {commit[:12]}; {differ} changed")
    return 0


def _catalog_version(path: Path) -> str:
    import yaml

    document = yaml.safe_load(path.read_text())
    return str(document["version"])


def _at(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def _price(snapshot, core_catalog, query: dict) -> dict:
    model = query["model"].strip().lower()
    at = _at(query["at"])
    # Replay's order: a gateway id under its creator spelled as written, then
    # under the creator's folded spelling; a bare id under "unknown".
    attempts = [("unknown", False)]
    if "/" in model:
        creator = model.split("/", 1)[0]
        attempts = [(creator, True), (creator, False)]
    cached = query.get("cachedInputTokens") or 0
    result = None
    for provider, as_written in attempts:
        result = _cost(snapshot, core_catalog, provider, as_written, model, at, query, cached)
        if result.reasons[:1] != ("unknown_model",):
            break
    assert result is not None
    return {
        "canonicalModel": result.canonical_model,
        "priceId": result.price_id,
        "costUsd": None if result.cost_usd is None else str(result.cost_usd),
        "status": result.status,
        "reasons": list(result.reasons),
    }


def _cost(snapshot, core_catalog, provider, as_written, model, at, query, cached):
    folds = core_catalog._PROVIDER_ALIASES
    if as_written:
        core_catalog._PROVIDER_ALIASES = {}
    try:
        key = provider if as_written else folds.get(provider, provider)
        input_tokens = query["inputTokens"]
        says = query.get("inputIncludesCacheRead")
        if says is not None:
            row_includes = _row_includes(snapshot, core_catalog, key, model, at)
            if row_includes is not None and says != row_includes:
                input_tokens = max(0, input_tokens - cached) if says else input_tokens + cached
        return snapshot.cost(
            provider=key, model=model, at=at,
            input_tokens=input_tokens, output_tokens=query["outputTokens"],
            cache_read_tokens=cached,
        )
    finally:
        core_catalog._PROVIDER_ALIASES = folds


def _row_includes(snapshot, core_catalog, provider, model, at):
    """Whether the row a query resolves to counts cache reads inside input."""
    alias = snapshot._alias_for(snapshot._aliases.get((provider, model), ()), at)
    if alias is None:
        return None
    price = snapshot._price_for(alias, at)
    if isinstance(price, tuple):
        price = price[0]
    if price is None:
        return None
    rules = {**dict(price.rules), **dict(alias.rules)}
    return core_catalog.counts_cache_read_in_input(price.publisher, price.pricing_channel, rules)


if __name__ == "__main__":
    sys.exit(main())
