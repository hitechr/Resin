import assert from "node:assert/strict";
import test from "node:test";
import { isValidMaxReferenceLatencyInput, parseMaxReferenceLatencyMs } from "./formParsers.ts";

test("empty max reference latency input disables the cap (0)", () => {
  assert.equal(parseMaxReferenceLatencyMs(undefined), 0);
  assert.equal(parseMaxReferenceLatencyMs(""), 0);
  assert.equal(parseMaxReferenceLatencyMs("   "), 0);
  assert.equal(isValidMaxReferenceLatencyInput(undefined), true);
  assert.equal(isValidMaxReferenceLatencyInput(""), true);
});

test("non-negative integers are accepted and mapped as-is", () => {
  assert.equal(isValidMaxReferenceLatencyInput("400"), true);
  assert.equal(parseMaxReferenceLatencyMs("400"), 400);
  assert.equal(parseMaxReferenceLatencyMs(" 400 "), 400);
  assert.equal(isValidMaxReferenceLatencyInput("0"), true);
  assert.equal(parseMaxReferenceLatencyMs("0"), 0);
});

test("negative, fractional, non-numeric and out-of-range inputs are rejected", () => {
  for (const input of ["-5", "1.5", "abc", "4 0", "1e3", "+400", "99999999999999999999"]) {
    assert.equal(isValidMaxReferenceLatencyInput(input), false, `input ${JSON.stringify(input)}`);
  }
});
