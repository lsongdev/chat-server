import { useEffect, useState } from 'react';
import { useChatClient } from '../useChatClient';
import type { User } from '../types';
import ChatModule from '../components/ChatModule';
import ContactsModule from '../components/ContactsModule';
import AppSettingsModule from '../components/AppSettingsModule';

type ModuleID = 'chat-module' | 'contacts-module' | 'app-settings-module';

const MODULES: { id: ModuleID; label: string; icon: string }[] = [
  {
    id: 'chat-module',
    label: 'Chat',
    icon: 'M21 15a4 4 0 0 1-4 4H8l-5 3V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4z',
  },
  {
    id: 'contacts-module',
    label: 'Contacts',
    icon: 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M22 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75',
  },
];

function userLabel(user: User): string {
  return user.email || user.display_name || user.username || user.id;
}

export default function ChatPage({ user }: { user: User }) {
  const chat = useChatClient();
  const [activeModule, setActiveModule] = useState<ModuleID>('chat-module');

  useEffect(() => {
    let active = true;
    chat
      .bootstrap(user)
      .then(() => {
        if (active) chat.connect();
      })
      .catch((error: Error) => chat.showNotice(error.message, true));
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const switchModule = async (moduleID: ModuleID) => {
    setActiveModule(moduleID);
    if (moduleID === 'contacts-module') {
      await chat.loadContacts().catch((error: Error) => chat.showNotice(error.message, true));
    }
  };

  return (
    <div className="app-shell">
      <nav className="module-nav" aria-label="主导航">
        <a className="nav-logo" href="/" aria-label="Chat">
          <img src="https://my.lsong.org/icon-192.png?v=2" alt="" />
        </a>
        {MODULES.map((mod) => (
          <button
            key={mod.id}
            className={`module-button${activeModule === mod.id ? ' active' : ''}`}
            type="button"
            data-module={mod.id}
            aria-label={mod.label}
            onClick={() => switchModule(mod.id).catch((error: Error) => chat.showNotice(error.message, true))}
          >
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d={mod.icon} />
            </svg>
          </button>
        ))}
        <div className="nav-spacer" />
        <button
          className={`module-button${activeModule === 'app-settings-module' ? ' active' : ''}`}
          type="button"
          data-module="app-settings-module"
          aria-label="Settings"
          onClick={() => switchModule('app-settings-module')}
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <circle cx="12" cy="12" r="3" />
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06-2.83 2.83-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21h-4v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06-2.83-2.83.06-.06A1.65 1.65 0 0 0 4.6 15a1.65 1.65 0 0 0-1.51-1H3v-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06 2.83-2.83.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3h4v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06 2.83 2.83-.06.06A1.65 1.65 0 0 0 19.4 9c.12.6.65 1.03 1.26 1.03H21v4h-.34c-.61 0-1.14.42-1.26.97z" />
          </svg>
        </button>
        <img
          className="avatar nav-avatar"
          alt=""
          title={userLabel(user)}
          referrerPolicy="no-referrer"
          src={user.avatar_url}
        />
      </nav>
      <div className="workspace">
        {chat.notice && (
          <div id="notice" className={chat.notice.error ? 'error' : ''} role="status" aria-live="polite">
            {chat.notice.message}
          </div>
        )}
        {activeModule === 'chat-module' && <ChatModule chat={chat} />}
        {activeModule === 'contacts-module' && <ContactsModule chat={chat} />}
        {activeModule === 'app-settings-module' && <AppSettingsModule chat={chat} />}
      </div>
    </div>
  );
}
