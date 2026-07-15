import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import * as client from "../api/client";
import type { Note } from "../types/api";
import { ComposeDialog } from "./ComposeDialog";

describe("ComposeDialog", () => {
  it("publishes text and OCR assets without a request-body author id", async () => {
    const created = { id: 123, title: "发布测试" } as Note;
    const api = vi.spyOn(client, "apiFetch").mockResolvedValue(created);
    const onCreated = vi.fn();
    render(<ComposeDialog open onClose={vi.fn()} onCreated={onCreated} onToast={vi.fn()} />);

    fireEvent.change(screen.getByLabelText("标题"), { target: { value: " 发布测试 " } });
    fireEvent.change(screen.getByLabelText("正文"), { target: { value: " 完整正文 " } });
    fireEvent.change(screen.getByLabelText("媒体 OCR / caption 文字"), { target: { value: " OCR 证据 " } });
    fireEvent.change(screen.getByLabelText("标签"), { target: { value: "检索, 评测" } });
    fireEvent.change(screen.getByLabelText("城市"), { target: { value: "上海" } });
    fireEvent.click(screen.getByRole("button", { name: "发布笔记" }));

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(created));
    const [, init] = api.mock.calls[0];
    const body = JSON.parse(String(init?.body));
    expect(body.author_id).toBeUndefined();
    expect(body.media[0].ocr_text).toBe("OCR 证据");
    expect(body.tags).toEqual(["检索", "评测"]);
  });
});
