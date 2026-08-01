import { useEffect, useState } from 'react';
import { api } from './api';
import ChatPage from './pages/ChatPage';
import InvitePage from './pages/InvitePage';
import LoginPage from './pages/LoginPage';
import type { User } from './types';

function useRoute(): string {
  const [path, setPath] = useState(() => window.location.pathname);
  useEffect(() => {
    const onPop = () => setPath(window.location.pathname);
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, []);
  return path;
}

export default function App() {
  const path = useRoute();
  const [user, setUser] = useState<User | null | undefined>(undefined);

  useEffect(() => {
    let active = true;
    api<User>('/api/me')
      .then((me) => {
        if (active) setUser(me);
      })
      .catch(() => {
        if (active) setUser(null);
      });
    return () => {
      active = false;
    };
  }, []);

  const inviteMatch = path.match(/^\/invite\/([^/]+)$/);
  const token = inviteMatch ? inviteMatch[1] : null;

  if (user === undefined) {
    return <div className="page" />;
  }
  if (!user) {
    return <LoginPage returnTo={token ? path : undefined} />;
  }
  if (token) {
    return <InvitePage token={token} />;
  }
  return <ChatPage user={user} />;
}
