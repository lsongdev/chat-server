import { useCallback, useEffect, useState } from 'react';
import { api } from './api';
import ChatPage from './pages/ChatPage';
import LoginPage from './pages/LoginPage';
import type { User } from './types';

export type Navigate = (path: string, options?: { replace?: boolean }) => void;

function useRoute(): { path: string; navigate: Navigate } {
  const [path, setPath] = useState(() => window.location.pathname);
  useEffect(() => {
    const onPop = () => setPath(window.location.pathname);
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, []);
  const navigate = useCallback<Navigate>((nextPath, options) => {
    if (window.location.pathname === nextPath) return;
    window.history[options?.replace ? 'replaceState' : 'pushState']({}, '', nextPath);
    setPath(nextPath);
  }, []);
  return { path, navigate };
}

export default function App() {
  const { path, navigate } = useRoute();
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

  if (user === undefined) {
    return <div className="page" />;
  }
  if (!user) {
    return <LoginPage />;
  }
  return <ChatPage user={user} path={path} navigate={navigate} />;
}
