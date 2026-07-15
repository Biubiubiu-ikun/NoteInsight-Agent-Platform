import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import * as client from "../api/client";
import type { Note, Page } from "../types/api";
import { FeedView } from "./FeedView";

describe("FeedView", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("renders API notes and opens the selected note", async () => {
    const note = noteFixture();
    vi.spyOn(client, "apiFetch").mockResolvedValue({ items: [note] } as Page<Note>);
    const onOpen = vi.fn();

    render(<FeedView category="study" query="检索" onOpen={onOpen} refreshKey={0} />);

    const card = await screen.findByRole("button", { name: `打开笔记：${note.title}` });
    fireEvent.click(card);
    expect(onOpen).toHaveBeenCalledWith(note.id);
    expect(client.apiFetch).toHaveBeenCalledWith(expect.stringContaining("category=study"));
    expect(client.apiFetch).toHaveBeenCalledWith(expect.stringContaining("q=%E6%A3%80%E7%B4%A2"));
  });

  it("shows an error and retries the request", async () => {
    const api = vi.spyOn(client, "apiFetch")
      .mockRejectedValueOnce(new client.ApiError("服务暂不可用", 503))
      .mockResolvedValueOnce({ items: [noteFixture()] } as Page<Note>);

    render(<FeedView category="" query="" onOpen={vi.fn()} refreshKey={0} />);
    expect(await screen.findByText("服务暂不可用")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    await waitFor(() => expect(api).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("检索测试笔记")).toBeInTheDocument();
  });
});

function noteFixture(): Note {
  return {
    id: 42,
    project_id: 1,
    author_id: 7,
    title: "检索测试笔记",
    body: "正文",
    category: "study",
    topics: [],
    tags: ["测试"],
    location: { city: "上海" },
    product_entities: [],
    note_type: "image_text",
    view_count: 10,
    like_count: 2,
    collect_count: 1,
    comment_count: 3,
    share_count: 0,
    hot_score: 1,
    quality_score: 0.9,
    status: "published",
    visibility: "public",
    content_version: 1,
    viewer_liked: false,
    viewer_collected: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}
