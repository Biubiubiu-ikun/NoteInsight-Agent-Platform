import { FileText, LoaderCircle, Sparkles, X } from "lucide-react";
import { useEffect, useState } from "react";
import { ApiError, apiFetch } from "../api/client";
import { categories } from "../lib/display";
import type { Note } from "../types/api";

interface ComposeDialogProps {
  open: boolean;
  onClose: () => void;
  onCreated: (note: Note) => void;
  onToast: (message: string) => void;
}

export function ComposeDialog({ open, onClose, onCreated, onToast }: ComposeDialogProps) {
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [category, setCategory] = useState("study");
  const [tags, setTags] = useState("");
  const [city, setCity] = useState("");
  const [ocrText, setOcrText] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (open) setError("");
  }, [open]);

  if (!open) return null;

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      const created = await apiFetch<Note>("/api/v1/notes", {
        method: "POST",
        body: JSON.stringify({
          project_id: 0,
          title: title.trim(),
          body: body.trim(),
          category,
          tags: tags.split(/[，,]/).map((tag) => tag.trim()).filter(Boolean),
          topics: [],
          location: city.trim() ? { city: city.trim(), synthetic: false } : {},
          product_entities: [],
          media: ocrText.trim() ? [{
            media_type: "image",
            url: "",
            caption: "用户提交的图文卡片文字",
            ocr_text: ocrText.trim(),
            position: 1,
            metadata: { visual_placeholder: true, source: "web_console" },
          }] : [],
        }),
      });
      onCreated(created);
      onToast("笔记已发布，作者身份来自当前 JWT");
      setTitle("");
      setBody("");
      setTags("");
      setCity("");
      setOcrText("");
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "发布失败");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="dialog-backdrop" role="presentation" onMouseDown={onClose}>
      <section className="dialog compose-dialog" role="dialog" aria-modal="true" aria-labelledby="compose-title" onMouseDown={(event) => event.stopPropagation()}>
        <div className="compose-header">
          <div><span className="eyebrow"><Sparkles size={14} />NEW NOTE</span><h2 id="compose-title">发布一篇图文笔记</h2></div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="关闭"><X size={20} /></button>
        </div>
        <form className="compose-layout" onSubmit={submit}>
          <div className="compose-main form-stack">
            <label><span>标题</span><input required maxLength={160} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="用明确结论说明这篇笔记的价值" /></label>
            <label><span>正文</span><textarea required rows={13} value={body} onChange={(e) => setBody(e.target.value)} placeholder="记录背景、过程、结果、限制和适用人群..." /></label>
            <label><span>媒体 OCR / caption 文字</span><textarea rows={5} value={ocrText} onChange={(e) => setOcrText(e.target.value)} placeholder="即使暂时没有真实图片，也可以先提交后续 RAG 可检索的卡片文字" /></label>
          </div>
          <aside className="compose-settings form-stack">
            <div className="compose-placeholder"><FileText size={28} /><strong>文字资产优先</strong><span>媒体 URL 可为空，OCR 文本会写入 note_media。</span></div>
            <label><span>分类</span><select value={category} onChange={(e) => setCategory(e.target.value)}>{categories.filter((item) => item.value).map((item) => <option value={item.value} key={item.value}>{item.label}</option>)}</select></label>
            <label><span>标签</span><input value={tags} onChange={(e) => setTags(e.target.value)} placeholder="实测, 复盘, 清单" /></label>
            <label><span>城市</span><input value={city} onChange={(e) => setCity(e.target.value)} placeholder="可选" /></label>
            {error && <p className="form-error" role="alert">{error}</p>}
            <button className="primary-button wide-button" type="submit" disabled={submitting}>{submitting && <LoaderCircle className="spin" size={17} />}发布笔记</button>
          </aside>
        </form>
      </section>
    </div>
  );
}
