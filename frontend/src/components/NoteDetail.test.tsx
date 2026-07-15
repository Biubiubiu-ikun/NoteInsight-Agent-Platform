import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import * as client from "../api/client";
import type { Note, NoteComment, Page } from "../types/api";
import { NoteDetail } from "./NoteDetail";

vi.mock("../auth/AuthContext", () => ({
  useAuth: () => ({ user: { id: 7, username: "owner", role: "normal", status: "active" } }),
}));

describe("NoteDetail", () => {
  beforeEach(() => vi.restoreAllMocks());

  it("loads evidence text and completes comment, like, and collect actions", async () => {
    const note = detailNote();
    const comments: Page<NoteComment> = { items: [] };
    const createdComment = {
      id: 901,
      note_id: note.id,
      user_id: 7,
      parent_id: 0,
      root_id: 0,
      content: "组件测试评论",
      like_count: 0,
      reply_count: 0,
      status: 1,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    } satisfies NoteComment;
    const api = vi.spyOn(client, "apiFetch").mockImplementation(async (path, init) => {
      if (path.endsWith("/comments?limit=20")) return comments;
      if (path.endsWith("/comments") && init?.method === "POST") return createdComment;
      if (path.endsWith("/like")) return { applied: true, count: 0, count_pending: true };
      if (path.endsWith("/collect")) return { applied: true, count: 0, count_pending: true };
      return note;
    });
    const onToast = vi.fn();
    render(<NoteDetail noteId={note.id} onClose={vi.fn()} onNeedAuth={vi.fn()} onToast={onToast} onChanged={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "笔记详情" });
    expect(within(dialog).getByRole("heading", { name: note.title })).toBeVisible();
    expect(within(dialog).getByText("可引用 OCR 文本")).toBeVisible();

    fireEvent.change(within(dialog).getByPlaceholderText("写下你的验证结果或疑问"), { target: { value: "组件测试评论" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "发布" }));
    expect(await within(dialog).findByText("组件测试评论")).toBeVisible();
    fireEvent.click(within(dialog).getByRole("button", { name: "点赞笔记" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "收藏笔记" }));
    await waitFor(() => expect(onToast).toHaveBeenCalledWith("已收藏"));
    expect(api).toHaveBeenCalledWith(`/api/v1/notes/${note.id}/like`, expect.objectContaining({ method: "POST" }));
  });
});

function detailNote(): Note {
  return {
    id: 77,
    project_id: 1,
    author_id: 7,
    title: "详情组件测试",
    body: "完整正文",
    category: "study",
    topics: [],
    tags: ["证据"],
    location: {},
    product_entities: [],
    note_type: "image_text",
    view_count: 0,
    like_count: 0,
    collect_count: 0,
    comment_count: 0,
    share_count: 0,
    hot_score: 0,
    quality_score: 0.9,
    status: "published",
    visibility: "public",
    content_version: 1,
    viewer_liked: false,
    viewer_collected: false,
    author: { id: 7, username: "owner", nickname: "Owner", avatar_url: "" },
    media: [{
      id: 1,
      note_id: 77,
      media_type: "image",
      url: "",
      caption: "结构化媒体",
      ocr_text: "可引用 OCR 文本",
      position: 1,
      metadata: {},
      created_at: "2026-01-01T00:00:00Z",
    }],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}
