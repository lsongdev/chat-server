import { useCallback, useEffect, useReducer, useRef } from 'react';
import { api } from './api';
import type { Contact, Conversation, Event, Member, ServerMessage, User, UserLookup } from './types';

type EventsMap = Record<string, Record<string, Event>>;

export interface Notice {
  message: string;
  error: boolean;
}

interface ChatState {
  user: User | null;
  conversations: Conversation[];
  selected: Conversation | null;
  members: Member[];
  contacts: Contact[];
  events: EventsMap;
  settingsOpen: boolean;
  connected: boolean;
  editingContactID: string | null;
  notice: Notice | null;
}

type Action =
  | { type: 'user'; user: User }
  | { type: 'conversations'; conversations: Conversation[] }
  | { type: 'selected'; selected: Conversation | null }
  | { type: 'members'; members: Member[] }
  | { type: 'contacts'; contacts: Contact[] }
  | { type: 'events'; conversation_id: string; events: Event[] }
  | { type: 'drop_events'; conversation_id: string }
  | { type: 'settings_open'; value: boolean }
  | { type: 'connected'; value: boolean }
  | { type: 'editing_contact'; value: string | null }
  | { type: 'notice'; value: Notice | null };

const initialState: ChatState = {
  user: null,
  conversations: [],
  selected: null,
  members: [],
  contacts: [],
  events: {},
  settingsOpen: false,
  connected: false,
  editingContactID: null,
  notice: null,
};

function mergeEvents(state: ChatState, conversationID: string, items: Event[]): EventsMap {
  const bucket = state.events[conversationID] || {};
  const next: Record<string, Event> = { ...bucket };
  for (const item of items) next[item.seq] = item;
  return { ...state.events, [conversationID]: next };
}

function reducer(state: ChatState, action: Action): ChatState {
  switch (action.type) {
    case 'user':
      return { ...state, user: action.user };
    case 'conversations':
      return { ...state, conversations: action.conversations };
    case 'selected':
      return { ...state, selected: action.selected, settingsOpen: false };
    case 'members':
      return { ...state, members: action.members };
    case 'contacts':
      return { ...state, contacts: action.contacts };
    case 'events':
      return { ...state, events: mergeEvents(state, action.conversation_id, action.events) };
    case 'drop_events': {
      const events = { ...state.events };
      delete events[action.conversation_id];
      return { ...state, events };
    }
    case 'settings_open':
      return { ...state, settingsOpen: action.value };
    case 'connected':
      return { ...state, connected: action.value };
    case 'editing_contact':
      return { ...state, editingContactID: action.value };
    case 'notice':
      return { ...state, notice: action.value };
  }
}

export function contiguousSeq(conversation: Conversation, bucket: Record<string, Event>): number {
  let cursor = conversation.joined_seq - 1;
  for (const seq of Object.keys(bucket)
    .map(Number)
    .sort((left, right) => left - right)) {
    if (seq === cursor + 1) cursor = seq;
    else if (seq > cursor + 1) break;
  }
  return cursor;
}

function openCache(dbName: string): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(dbName, 1);
    request.onupgradeneeded = () => {
      const store = request.result.createObjectStore('events', { keyPath: 'key' });
      store.createIndex('conversation', 'cache_conversation');
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

function cacheEvents(cache: IDBDatabase | null, events: Event[]): Promise<void> {
  if (!cache || !events.length) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const transaction = cache.transaction('events', 'readwrite');
    const store = transaction.objectStore('events');
    for (const item of events) {
      store.put({ ...item, key: `${item.conversation_id}|${item.seq}`, cache_conversation: item.conversation_id });
    }
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(transaction.error);
  });
}

function loadCachedEvents(cache: IDBDatabase | null, conversationID: string): Promise<Event[]> {
  if (!cache) return Promise.resolve([]);
  return new Promise((resolve, reject) => {
    const request = cache.transaction('events').objectStore('events').index('conversation').getAll(conversationID);
    request.onsuccess = () => resolve(request.result as Event[]);
    request.onerror = () => reject(request.error);
  });
}

export function useChatClient() {
  const [state, dispatch] = useReducer(reducer, initialState);
  const stateRef = useRef(state);
  const socketRef = useRef<WebSocket | null>(null);
  const reconnectRef = useRef(500);
  const cacheRef = useRef<IDBDatabase | null>(null);
  const readTimersRef = useRef(new Map<string, number>());
  const stateDispatch = useCallback((action: Action) => dispatch(action), []);

  useEffect(() => {
    stateRef.current = state;
  }, [state]);

  const showNotice = useCallback((message: string, error = false) => {
    dispatch({ type: 'notice', value: { message, error } });
  }, []);

  const refreshConversations = useCallback(async (): Promise<Conversation[]> => {
    const result = await api<{ conversations: Conversation[] }>('/api/conversations');
    dispatch({ type: 'conversations', conversations: result.conversations });
    return result.conversations;
  }, []);

  const syncEvents = useCallback(
    async (conversationID: string, afterSeq: number) => {
      let cursor = afterSeq;
      for (;;) {
        const result = await api<{ events: Event[] }>(
          `/api/conversations/${conversationID}/events?after_seq=${cursor}&limit=200`,
        );
        dispatch({ type: 'events', conversation_id: conversationID, events: result.events });
        await cacheEvents(cacheRef.current, result.events);
        for (const item of result.events) cursor = Math.max(cursor, item.seq);
        if (result.events.length < 200) break;
      }
    },
    [],
  );

  const loadMembers = useCallback(async (conversation: Conversation): Promise<Member[]> => {
    const result = await api<{ members: Member[] }>(`/api/conversations/${conversation.id}/members`);
    dispatch({ type: 'members', members: result.members });
    return result.members;
  }, []);

  const selectConversation = useCallback(
    async (conversation: Conversation | null) => {
      dispatch({ type: 'selected', selected: conversation });
      if (!conversation) return;
      await loadMembers(conversation).catch((error: Error) => showNotice(error.message, true));
      const current = stateRef.current;
      let localBucket = current.events[conversation.id];
      if (!localBucket) {
        const cached = await loadCachedEvents(cacheRef.current, conversation.id);
        dispatch({ type: 'events', conversation_id: conversation.id, events: cached });
        localBucket = {};
        for (const item of cached) localBucket[item.seq] = item;
      }
      await syncEvents(conversation.id, contiguousSeq(conversation, localBucket));
    },
    [loadMembers, showNotice, syncEvents],
  );

  const createConversation = useCallback(
    async (title: string) => {
      const conversation = await api<Conversation>('/api/conversations', {
        method: 'POST',
        body: JSON.stringify({ title }),
      });
      const conversations = await refreshConversations();
      const fresh = conversations.find((item) => item.id === conversation.id) || conversation;
      await selectConversation(fresh);
      return conversation;
    },
    [refreshConversations, selectConversation],
  );

  const sendMessage = useCallback((text: string): boolean => {
    const current = stateRef.current;
    const socket = socketRef.current;
    const conversation = current.selected;
    if (!text || !socket || socket.readyState !== WebSocket.OPEN || !conversation) return false;
    socket.send(
      JSON.stringify({
        type: 'message.send',
        request_id: crypto.randomUUID(),
        conversation_id: conversation.id,
        client_message_id: crypto.randomUUID(),
        content: { text },
      }),
    );
    return true;
  }, []);

  const updateRead = useCallback((conversation: Conversation, readSeq: number) => {
    if (readSeq < conversation.joined_seq) return;
    const key = conversation.id;
    const existing = readTimersRef.current.get(key);
    if (existing) window.clearTimeout(existing);
    const timer = window.setTimeout(async () => {
      try {
        await api(`/api/conversations/${conversation.id}/read`, {
          method: 'POST',
          body: JSON.stringify({ seq: readSeq }),
        });
        const current = stateRef.current;
        const entry = current.conversations.find((item) => item.id === conversation.id);
        if (entry) {
          const updated = current.conversations.map((item) =>
            item.id === conversation.id
              ? { ...item, last_read_seq: Math.max(item.last_read_seq, readSeq) }
              : item,
          );
          dispatch({ type: 'conversations', conversations: updated });
        }
      } catch {
        /* read cursor is best-effort */
      }
      readTimersRef.current.delete(key);
    }, 1000);
    readTimersRef.current.set(key, timer);
  }, []);

  const connect = useCallback(() => {
    const existing = socketRef.current;
    if (existing && (existing.readyState === WebSocket.OPEN || existing.readyState === WebSocket.CONNECTING)) return;
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const socket = new WebSocket(`${protocol}//${location.host}/ws`);
    socketRef.current = socket;
    socket.onopen = () => {
      reconnectRef.current = 500;
      dispatch({ type: 'connected', value: true });
    };
    socket.onclose = () => {
      dispatch({ type: 'connected', value: false });
      window.setTimeout(connect, reconnectRef.current);
      reconnectRef.current = Math.min(reconnectRef.current * 2, 10000);
    };
    socket.onmessage = async ({ data }) => {
      const message = JSON.parse(data as string) as ServerMessage;
      if (message.type === 'conversation.event') {
        const item = message.event;
        const current = stateRef.current;
        const bucket = current.events[item.conversation_id] || {};
        const known = Object.keys(bucket).length ? Math.max(...Object.keys(bucket).map(Number)) : 0;
        if (known && item.seq > known + 1) await syncEvents(item.conversation_id, known);
        dispatch({ type: 'events', conversation_id: item.conversation_id, events: [item] });
        await cacheEvents(cacheRef.current, [item]);
        let conversations = stateRef.current.conversations;
        let conversation = conversations.find((entry) => entry.id === item.conversation_id);
        if (!conversation) {
          conversations = await refreshConversations();
          conversation = conversations.find((entry) => entry.id === item.conversation_id);
        }
        if (conversation) {
          const updated = conversations.map((entry) =>
            entry.id === item.conversation_id
              ? {
                  ...entry,
                  last_seq: Math.max(entry.last_seq, item.seq),
                  title: item.type === 'conversation.renamed' ? String(item.payload.title ?? entry.title) : entry.title,
                }
              : entry,
          );
          dispatch({ type: 'conversations', conversations: updated });
        }
        const latest = stateRef.current;
        if (latest.selected?.id === item.conversation_id && item.type.startsWith('member.')) {
          await loadMembers(latest.selected);
        }
      } else if (message.type === 'conversation.deleted') {
        dispatch({ type: 'drop_events', conversation_id: message.conversation_id });
        const latest = stateRef.current;
        if (latest.selected?.id === message.conversation_id) dispatch({ type: 'selected', selected: null });
        await refreshConversations();
      } else if (message.type === 'error') {
        showNotice(message.code || '请求失败', true);
      }
    };
  }, [loadMembers, refreshConversations, showNotice, syncEvents]);

  const loadContacts = useCallback(async () => {
    const result = await api<{ contacts: Contact[] }>('/api/contacts');
    dispatch({ type: 'contacts', contacts: result.contacts });
  }, []);

  const saveContact = useCallback(
    async (input: { id?: string; name: string; email: string; note: string }) => {
      const endpoint = input.id ? `/api/contacts/${input.id}` : '/api/contacts';
      const method = input.id ? 'PUT' : 'POST';
      await api(endpoint, {
        method,
        body: JSON.stringify({ name: input.name, email: input.email, note: input.note }),
      });
      await loadContacts();
    },
    [loadContacts],
  );

  const deleteContact = useCallback(
    async (id: string) => {
      await api(`/api/contacts/${id}`, { method: 'DELETE' });
      await loadContacts();
    },
    [loadContacts],
  );

  const searchUser = useCallback(async (email: string) => {
    return api<UserLookup>(`/api/users/search?email=${encodeURIComponent(email)}`);
  }, []);

  const addMember = useCallback(
    async (conversation: Conversation, email: string) => {
      await api(`/api/conversations/${conversation.id}/members`, {
        method: 'POST',
        body: JSON.stringify({ email }),
      });
      await loadMembers(conversation);
    },
    [loadMembers],
  );

  const renameConversation = useCallback(async (conversation: Conversation, title: string) => {
    await api(`/api/conversations/${conversation.id}`, { method: 'PATCH', body: JSON.stringify({ title }) });
  }, []);

  const removeMember = useCallback(
    async (conversation: Conversation, userID: string) => {
      await api(`/api/conversations/${conversation.id}/members/${userID}`, { method: 'DELETE' });
      await loadMembers(conversation);
    },
    [loadMembers],
  );

  const updateMemberRole = useCallback(
    async (conversation: Conversation, userID: string, role: string) => {
      await api(`/api/conversations/${conversation.id}/members/${userID}`, {
        method: 'PATCH',
        body: JSON.stringify({ role }),
      });
      await loadMembers(conversation);
    },
    [loadMembers],
  );

  const leaveConversation = useCallback(
    async (conversation: Conversation) => {
      await api(`/api/conversations/${conversation.id}/leave`, { method: 'POST' });
      dispatch({ type: 'drop_events', conversation_id: conversation.id });
      if (stateRef.current.selected?.id === conversation.id) dispatch({ type: 'selected', selected: null });
      await refreshConversations();
    },
    [refreshConversations],
  );

  const deleteConversation = useCallback(
    async (conversation: Conversation) => {
      await api(`/api/conversations/${conversation.id}`, { method: 'DELETE' });
      dispatch({ type: 'drop_events', conversation_id: conversation.id });
      if (stateRef.current.selected?.id === conversation.id) dispatch({ type: 'selected', selected: null });
      await refreshConversations();
    },
    [refreshConversations],
  );

  const createInvite = useCallback(async (conversation: Conversation) => {
    return api<{ url: string; expires_at: string }>(`/api/conversations/${conversation.id}/invites`, {
      method: 'POST',
      body: '{}',
    });
  }, []);

  const logout = useCallback(async () => {
    await api('/auth/logout', { method: 'POST', body: '{}' });
    if (cacheRef.current) cacheRef.current.close();
    const user = stateRef.current.user;
    if (user) indexedDB.deleteDatabase(`chat-${user.id}`);
    location.href = '/login';
  }, []);

  const bootstrap = useCallback(
    async (user: User) => {
      dispatch({ type: 'user', user });
      cacheRef.current = await openCache(`chat-${user.id}`).catch(() => null);
      const conversations = await refreshConversations();
      return conversations;
    },
    [refreshConversations],
  );

  return {
    ...state,
    dispatch: stateDispatch,
    showNotice,
    bootstrap,
    refreshConversations,
    selectConversation,
    createConversation,
    sendMessage,
    updateRead,
    connect,
    loadContacts,
    saveContact,
    deleteContact,
    searchUser,
    addMember,
    renameConversation,
    removeMember,
    updateMemberRole,
    leaveConversation,
    deleteConversation,
    createInvite,
    logout,
  };
}

export type ChatClient = ReturnType<typeof useChatClient>;
