package lane

// LaneOutputContract is the lane-only structured output instruction appended to
// initial and retry prompts.
const LaneOutputContract = `Return exactly one JSON object in a single fenced block. The object must contain:
- "laneId": a string that echoes the current lane ID verbatim.
- "findings": an array, which may be empty.

Every finding must contain "title", "severity", and "rationale". "severity" must be one of "high", "medium", or "low". A finding may also contain "file", "line", "endLine", "category", "suggestion", "summary", "positives", "notes", and "citations". Each citation is an object with "sourceId" and "label".

No prose outside the JSON fenced block is needed.`
