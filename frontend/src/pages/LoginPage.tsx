import { safeReturnTo } from '../api';

export default function LoginPage({ returnTo: returnToOverride }: { returnTo?: string }) {
  const returnTo = safeReturnTo(returnToOverride || new URLSearchParams(window.location.search).get('return_to'));

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
              使用 MyCenter 安全登录并同步聊天记录。
            </p>
            <a id="oidc-login" className="button" href={`/auth/login?return_to=${encodeURIComponent(returnTo)}`}>
              使用 MyCenter 登录
            </a>
          </section>
        </main>
        <footer className="login-footer">Simple, private chat.</footer>
      </div>
    </div>
  );
}
