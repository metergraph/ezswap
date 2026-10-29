import { test } from "node:test";
import assert from "node:assert/strict";
import type { RunFile } from "../src/types.ts";

test("the run schema name is stable", () => {
  const schema: RunFile["schema"] = "metergraph.replay-run/v1";
  assert.equal(schema, "metergraph.replay-run/v1");
});
