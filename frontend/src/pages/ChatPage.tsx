import { useEffect, useState } from 'react';
import type { Navigate } from '../App';
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

function routeModule(path: string): ModuleID {
  if (path === '/contacts') return 'contacts-module';
  if (path === '/settings') return 'app-settings-module';
  return 'chat-module';
}

function routeIsValid(path: string): boolean {
  return path === '/chat' || path === '/contacts' || path === '/settings' || /^\/chat\/[^/]+$/.test(path);
}

export default function ChatPage({ user, path, navigate }: { user: User; path: string; navigate: Navigate }) {
  const chat = useChatClient();
  const [ready, setReady] = useState(false);
  const { bootstrap, connect, conversations, dispatch, loadContacts, selectConversation, selected, showNotice } = chat;
  const activeModule = routeModule(path);
  const conversationMatch = path.match(/^\/chat\/([^/]+)$/);
  const conversationID = conversationMatch?.[1] || null;

  useEffect(() => {
    if (!routeIsValid(path)) navigate('/chat', { replace: true });
  }, [navigate, path]);

  useEffect(() => {
    let active = true;
    bootstrap(user)
      .then(() => {
        if (active) {
          setReady(true);
          connect();
        }
      })
      .catch((error: Error) => showNotice(error.message, true));
    return () => {
      active = false;
    };
  }, [bootstrap, connect, showNotice, user]);

  useEffect(() => {
    if (!ready || activeModule !== 'contacts-module') return;
    loadContacts().catch((error: Error) => showNotice(error.message, true));
  }, [activeModule, loadContacts, ready, showNotice]);

  useEffect(() => {
    if (!ready || activeModule !== 'chat-module') return;
    if (!conversationID) {
      if (selected) dispatch({ type: 'selected', selected: null });
      return;
    }
    const conversation = conversations.find((item) => item.id === conversationID);
    if (!conversation) {
      showNotice('会话不存在、已删除或你已经离开。', true);
      navigate('/chat', { replace: true });
      return;
    }
    if (selected?.id !== conversation.id) {
      selectConversation(conversation).catch((error: Error) => showNotice(error.message, true));
    }
  }, [activeModule, conversationID, conversations, dispatch, navigate, ready, selectConversation, selected, showNotice]);

  const modulePath = (moduleID: ModuleID): string => {
    if (moduleID === 'contacts-module') return '/contacts';
    if (moduleID === 'app-settings-module') return '/settings';
    return '/chat';
  };

  return (
    <div className="app-shell">
      <nav className="module-nav" aria-label="主导航">
        <a
          className="nav-logo"
          href="/chat"
          aria-label="Chat"
          onClick={(event) => {
            event.preventDefault();
            navigate('/chat');
          }}
        >
          <img src="https://my.lsong.org/icon-192.png?v=2" alt="" />
        </a>
        {MODULES.map((mod) => (
          <button
            key={mod.id}
            className={`module-button${activeModule === mod.id ? ' active' : ''}`}
            type="button"
            data-module={mod.id}
            aria-label={mod.label}
            onClick={() => navigate(modulePath(mod.id))}
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
          onClick={() => navigate('/settings')}
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
        {activeModule === 'chat-module' && (
          <ChatModule
            chat={chat}
            conversationRouteActive={conversationID !== null}
            onOpenConversation={(id) => navigate(`/chat/${id}`)}
            onBackToList={() => navigate('/chat')}
          />
        )}
        {activeModule === 'contacts-module' && <ContactsModule chat={chat} />}
        {activeModule === 'app-settings-module' && <AppSettingsModule chat={chat} />}
      </div>
    </div>
  );
}
