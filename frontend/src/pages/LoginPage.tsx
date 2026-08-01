import { useState } from 'react';
import { safeReturnTo } from '../api';

export default function LoginPage({ returnTo: returnToOverride }: { returnTo?: string }) {
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [status, setStatus] = useState({ message: '', error: false });
  const [busy, setBusy] = useState(false);

  const returnTo = safeReturnTo(returnToOverride || new URLSearchParams(window.location.search).get('return_to'));

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setStatus({ message: '', error: false });
    try {
      const response = await fetch('/auth/email', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: name.trim(), email: email.trim() }),
      });
      if (!response.ok) {
        const problem = (await response.json().catch(() => ({}))) as { error?: { message?: string } };
        throw new Error(problem.error?.message || `登录失败 (${response.status})`);
      }
      location.href = returnTo;
    } catch (error) {
      setStatus({ message: (error as Error).message, error: true });
      setBusy(false);
    }
  };

  return (
    <div className="login-body">
      <div className="login-shell">
        <header className="login-header">
          <a className="brand" href="/">
            <img className="logo" src="https://my.lsong.org/icon-192.png?v=2" alt="" />
            <span>Chat</span>
          </a>
          <a className="mycenter" href="https://my.lsong.org">
            MyCenter
          </a>
        </header>
        <main className="login-main">
          <section className="card login-card">
            <h1>欢迎回来</h1>
            <p className="muted" style={{ margin: '5px 0 18px' }}>
              输入姓名和邮箱即可开始聊天。
            </p>
            <form onSubmit={submit}>
              <div>
                <label htmlFor="name">姓名</label>
                <input
                  id="name"
                  name="name"
                  maxLength={80}
                  autoComplete="name"
                  required
                  autoFocus
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                />
              </div>
              <div>
                <label htmlFor="email">邮箱</label>
                <input
                  id="email"
                  name="email"
                  type="email"
                  autoComplete="email"
                  required
                  value={email}
                  onChange={(event) => setEmail(event.target.value)}
                />
              </div>
              <button type="submit" disabled={busy}>
                {busy ? '正在登录…' : '登录'}
              </button>
            </form>
            <p id="status" className={`status small ${status.error ? 'error' : ''}`} role="status" aria-live="polite">
              {status.message}
            </p>
            <div className="login-divider">或者</div>
            <a id="oidc-login" className="button" href={`/auth/login?return_to=${encodeURIComponent(returnTo)}`}>
              使用 MyCenter 获取邮箱
            </a>
          </section>
        </main>
        <footer className="login-footer">Simple chat, identified by email.</footer>
      </div>
    </div>
  );
}
