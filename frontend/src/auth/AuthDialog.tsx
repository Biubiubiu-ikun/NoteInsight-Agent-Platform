import { LoaderCircle, LockKeyhole, UserRound, X } from "lucide-react";
import { useEffect, useState } from "react";
import { ApiError } from "../api/client";
import { useAuth } from "./AuthContext";

interface AuthDialogProps {
  open: boolean;
  onClose: () => void;
}

export function AuthDialog({ open, onClose }: AuthDialogProps) {
  const { login, register } = useAuth();
  const [mode, setMode] = useState<"login" | "register">("login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [nickname, setNickname] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (open) setError("");
  }, [open, mode]);

  if (!open) return null;

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      if (mode === "login") await login(username.trim(), password);
      else await register(username.trim(), password, nickname.trim());
      onClose();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "暂时无法完成登录");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="dialog-backdrop" role="presentation" onMouseDown={onClose}>
      <section className="dialog auth-dialog" role="dialog" aria-modal="true" aria-labelledby="auth-title" onMouseDown={(e) => e.stopPropagation()}>
        <button className="icon-button dialog-close" type="button" onClick={onClose} aria-label="关闭">
          <X size={19} />
        </button>
        <div className="auth-mark"><LockKeyhole size={22} /></div>
        <h2 id="auth-title">{mode === "login" ? "登录 NoteInsight" : "创建测试账号"}</h2>
        <p className="muted">登录后即可发布、评论和验证完整互动链路。</p>

        <div className="segmented auth-segment" aria-label="账号操作">
          <button type="button" className={mode === "login" ? "active" : ""} onClick={() => setMode("login")}>登录</button>
          <button type="button" className={mode === "register" ? "active" : ""} onClick={() => setMode("register")}>注册</button>
        </div>

        <form className="form-stack" onSubmit={submit}>
          <label>
            <span>用户名</span>
            <div className="input-with-icon"><UserRound size={17} /><input required minLength={3} value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" /></div>
          </label>
          {mode === "register" && (
            <label>
              <span>昵称</span>
              <input value={nickname} onChange={(e) => setNickname(e.target.value)} placeholder="可选" />
            </label>
          )}
          <label>
            <span>密码</span>
            <div className="input-with-icon"><LockKeyhole size={17} /><input required minLength={8} type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete={mode === "login" ? "current-password" : "new-password"} /></div>
          </label>
          {error && <p className="form-error" role="alert">{error}</p>}
          <button className="primary-button wide-button" type="submit" disabled={submitting}>
            {submitting && <LoaderCircle className="spin" size={17} />}
            {mode === "login" ? "登录" : "注册并登录"}
          </button>
        </form>
      </section>
    </div>
  );
}
