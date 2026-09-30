import { describe, expect, it } from "vitest";
import { applicationName, foundationMessage } from "./app-info";

describe("application information", () => {
  it("identifies the application and its initial state", () => {
    expect(applicationName).toBe("Historical Usenet Indexer");
    expect(foundationMessage).toBe("Stage 1 foundation is running.");
  });
});
