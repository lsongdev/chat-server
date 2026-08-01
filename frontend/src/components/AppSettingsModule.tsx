import type { ChatClient } from '../useChatClient';

export default function AppSettingsModule({ chat }: { chat: ChatClient }) {
  const user = chat.user;
  return (
    <section className="module card page">
      <header className="page-head">
        <div>
          <h2>Settings</h2>
          <p className="muted small">账户和连接信息。</p>
        </div>
      </header>
      <div className="settings-list">
        <section className="settings-item">
          <h3>当前账户</h3>
          <p id="settings-identity">{user?.display_name || user?.username || 'Chat user'}</p>
          <p id="settings-email" className="muted small">
            {user?.email}
          </p>
        </section>
        <section className="settings-item">
          <h3>连接</h3>
          <p>HTTP + WebSocket protocol v1</p>
          <p id="settings-connection" className="muted small">
            {chat.connected ? '已连接' : '连接断开，正在重连'}
          </p>
        </section>
        <section className="settings-item">
          <h3>会话</h3>
          <button
            id="settings-logout"
            className="danger"
            type="button"
            onClick={() => chat.logout().catch((error: Error) => chat.showNotice(error.message, true))}
          >
            退出登录
          </button>
        </section>
      </div>
    </section>
  );
}
