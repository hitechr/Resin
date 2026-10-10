export function parseLinesToList(input: string | undefined, normalize?: (value: string) => string): string[] {
  if (!input) {
    return [];
  }

  return input
    .split(/\n/)
    .map((item) => item.trim())
    .filter(Boolean)
    .map((item) => (normalize ? normalize(item) : item));
}

export function parseHeaderLines(input: string | undefined): string[] {
  const lines = parseLinesToList(input);
  const seen = new Set<string>();
  const headers: string[] = [];
  for (const line of lines) {
    const key = line.toLowerCase();
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    headers.push(line);
  }
  return headers;
}

// parseMaxReferenceLatencyMs converts the optional form input to the API
// value. Empty input means the reference-latency cap is off (0).
export function parseMaxReferenceLatencyMs(input: string | undefined): number {
  const trimmed = input?.trim();
  if (!trimmed) {
    return 0;
  }
  return Number(trimmed);
}

// isValidMaxReferenceLatencyInput reports whether the raw form input is
// empty (cap off) or a non-negative safe integer.
export function isValidMaxReferenceLatencyInput(input: string | undefined): boolean {
  const trimmed = input?.trim();
  if (!trimmed) {
    return true;
  }
  return /^\d+$/.test(trimmed) && Number.isSafeInteger(Number(trimmed));
}
