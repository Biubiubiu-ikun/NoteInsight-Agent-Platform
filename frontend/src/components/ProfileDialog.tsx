import { LoaderCircle, LogOut, Save, X } from "lucide-react";
import { useEffect, useState } from "react";
import { ApiError } from "../api/client";
import { useAuth } from "../auth/AuthContext";

export function ProfileDialog({ open, onClose, onToast }: { open: boolean; onClose: () => void; onToast: (message: string) => void }) {
  const { user, logout, updateProfile } = useAuth();
  const [nickname, setNickname] = useState("");
  const [bio, setBio] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (user) {
      setNickname(user.nickname || "");
      setBio(user.bio || "");
    }
  }, [user, open]);

  if (!open || !user) return null;

  async function save(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      await updateProfile({ nickname: nickname.trim(), bio: bio.trim() });
      onToast("个人资料已更新");
      onClose();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "更新失败");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="dialog-backdrop" role="presentation" onMouseDown={onClose}>
      <section className="dialog profile-dialog" role="dialog" aria-modal="true" aria-label="账号资料" onMouseDown={(event) => event.stopPropagation()}>
        <button className="icon-button dialog-close" type="button" onClick={onClose} aria-label="关闭"><X size={19} /></button>
        <div className="profile-identity"><div className="avatar large-avatar">{(user.nickname || user.username).slice(0, 1).toUpperCase()}</div><div><h2>{user.nickname || user.username}</h2><p>@{user.username} · {user.role} · {user.status}</p></div></div>
        <form className="form-stack" onSubmit={save}>
          <label><span>昵称</span><input value={nickname} onChange={(e) => setNickname(e.target.value)} /></label>
          <label><span>简介</span><textarea rows={4} value={bio} onChange={(e) => setBio(e.target.value)} /></label>
          {error && <p className="form-error" role="alert">{error}</p>}
          <button className="primary-button wide-button" disabled={busy}>{busy ? <LoaderCircle className="spin" size={17} /> : <Save size={17} />}保存</button>
          <button className="secondary-button wide-button" type="button" onClick={async () => { await logout(); onClose(); onToast("已安全退出"); }}><LogOut size={17} />退出登录</button>
        </form>
      </section>
    </div>
  );
}
