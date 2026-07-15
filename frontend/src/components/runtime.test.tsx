import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as client from "../api/client";
import type { HotNoteItem, Note, Page } from "../types/api";
import { RankingView } from "./RankingView";
import { SystemView } from "./SystemView";

describe("runtime views", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("renders ranked notes returned by the ranking API", async () => {
    const note = runtimeNote();
    vi.spyOn(client, "apiFetch").mockResolvedValue({
      items: [{ note_id: note.id, score: 10, note }],
    } as Page<HotNoteItem>);
    const onOpen = vi.fn();
    render(<RankingView category="study" onOpen={onOpen} />);

    const card = await screen.findByRole("button", { name: `打开笔记：${note.title}` });
    fireEvent.click(card);
    expect(onOpen).toHaveBeenCalledWith(note.id);
    expect(client.apiFetch).toHaveBeenCalledWith(expect.stringContaining("category=study"));
  });

  it("parses service readiness and event-pipeline metrics", async () => {
    const metrics = [
      'outbox_events{status="pending"} 0',
      'outbox_events{status="failed"} 0',
      "outbox_oldest_unsent_age_seconds 0",
      "jetstream_consumer_pending_messages 0",
      "jetstream_consumer_ack_pending_messages 0",
      "nats_connected 1",
    ].join("\n");
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).endsWith("/metrics")) return new Response(metrics, { status: 200 });
      return new Response("{}", { status: 200 });
    }));

    render(<SystemView />);
    await waitFor(() => expect(screen.getAllByText("ready")).toHaveLength(3));
    expect(screen.getByText("connected")).toBeInTheDocument();
    expect(screen.getByText("Outbox 待发布").parentElement).toHaveTextContent("0");
    fireEvent.click(screen.getByRole("button", { name: "刷新" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(8));
  });
});

function runtimeNote(): Note {
  return {
    id: 88,
    project_id: 1,
    author_id: 7,
    title: "热榜测试笔记",
    body: "正文",
    category: "study",
    topics: [],
    tags: [],
    location: {},
    product_entities: [],
    note_type: "image_text",
    view_count: 1,
    like_count: 2,
    collect_count: 3,
    comment_count: 4,
    share_count: 5,
    hot_score: 10,
    quality_score: 0.8,
    status: "published",
    visibility: "public",
    content_version: 1,
    viewer_liked: false,
    viewer_collected: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}
