import { describe, it, expect } from "vitest";

describe("basic math", () => {
  it("adds numbers", () => {
    expect(1 + 2).toBe(3);
  });

  it("fails on purpose", () => {
    expect(1 + 1).toBe(3);
  });
});
