import { describe, expect, it, vi } from "vitest";
import { reorderLane, statusLabels, unknown, type Job } from "@/jobs";
import { setCSRFToken } from "@/api/client";

describe("task-center contracts", () => {
  it("renders unknown telemetry without inventing zero", () => {
    expect(unknown(null, "%")).toBe("未知");
    expect(unknown(0, "%")).toBe("0%");
    expect(statusLabels.waiting_user_action).toBe("等待操作");
  });

  it("sends the complete revisioned lane order", async () => {
    setCSRFToken("test-csrf");
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(
        new Response(
          JSON.stringify({ code: 0, message: "success", data: { list: [] } }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      );
    await reorderLane("download", 10, [
      { id: "b", revision: 3 },
      { id: "a", revision: 2 },
    ] as Job[]);
    const [path, options] = fetchMock.mock.calls[0];
    expect(path).toBe("/api/v1/job-lanes/download/10/order");
    expect(JSON.parse(String(options?.body))).toEqual({
      jobs: [
        { id: "b", revision: 3 },
        { id: "a", revision: 2 },
      ],
    });
    fetchMock.mockRestore();
  });
});
