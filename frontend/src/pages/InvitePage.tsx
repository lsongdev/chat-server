import { useState } from 'react';
import { api } from '../api';

export default function InvitePage({ token }: { token: string }) {
  const [status, setStatus] = useState({ message: '', error: false });
  const [busy, setBusy] = useState(false);

  const accept = async () => {
    setBusy(true);
    setStatus({ message: '正在加入…', error: false });
    try {
      await api(`/api/invites/${token}/accept`, { method: 'POST', body: '{}' });
      location.href = '/';
    } catch (error) {
      setStatus({ message: (error as Error).message, error: true });
      setBusy(false);
    }
  };

  return (
    <div className="invite-body">
      <header className="invite-header">
        <a className="brand" href="/">
          Chat
        </a>
      </header>
      <main className="invite-main">
        <section className="card invite-card">
          <h1>加入会话</h1>
          <p className="muted">确认后，你将成为这个会话的成员。</p>
          <div className="invite-actions">
            <button type="button" onClick={accept} disabled={busy}>
              确认加入
            </button>
            <a className="button" href="/">
              返回聊天
            </a>
          </div>
          <p id="status" className={`status ${status.error ? 'error' : ''}`} role="status" aria-live="polite">
            {status.message}
          </p>
        </section>
      </main>
    </div>
  );
}
