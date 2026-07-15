import { describe, expect, it } from "vitest";
import { coverPosition, formatCount } from "./display";

describe("display helpers", () => {
  it("formats ordinary and compact counts", () => {
    expect(formatCount(999)).toBe("999");
    expect(formatCount(1_500)).toContain("1.5");
  });

  it("maps known categories to stable sprite positions", () => {
    expect(coverPosition("beauty")).toBe("0% 100%");
    expect(coverPosition("digital")).toBe("100% 0%");
  });

  it("uses a deterministic fallback position", () => {
    expect(coverPosition("unknown")).toBe("50% 100%");
  });
});
