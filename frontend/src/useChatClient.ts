import { useCallback, useEffect, useReducer, useRef } from 'react';
import { APIError, api } from './api';
import { RealtimeClient, type DeliveryEvent } from './realtime';
import type { Contact, Conversation, Event, Member, User } from './types';

type EventsMap = Record<string, Record<string, Event>>;

interface CachedPublish {
  id: string;
  room_id: string;
  name: string;
  profile: 'durable';
  data: Record<string, unknown>;
}

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

async function retryNetwork<T>(operation: () => Promise<T>): Promise<T> {
  try {
    return await operation();
  } catch (error) {
    if (error instanceof APIError) throw error;
    return operation();
  }
}

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
    const request = indexedDB.open(dbName, 2);
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains('events')) {
        const store = request.result.createObjectStore('events', { keyPath: 'key' });
        store.createIndex('conversation', 'cache_conversation');
      }
      if (!request.result.objectStoreNames.contains('outbox')) {
        request.result.createObjectStore('outbox', { keyPath: 'id' });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

function saveCachedPublish(cache: IDBDatabase | null, publish: CachedPublish): Promise<void> {
  if (!cache) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const transaction = cache.transaction('outbox', 'readwrite');
    transaction.objectStore('outbox').put(publish);
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(transaction.error);
  });
}

function deleteCachedPublish(cache: IDBDatabase | null, id: string): Promise<void> {
  if (!cache) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const transaction = cache.transaction('outbox', 'readwrite');
    transaction.objectStore('outbox').delete(id);
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(transaction.error);
  });
}

function loadCachedPublishes(cache: IDBDatabase | null): Promise<CachedPublish[]> {
  if (!cache) return Promise.resolve([]);
  return new Promise((resolve, reject) => {
    const request = cache.transaction('outbox').objectStore('outbox').getAll();
    request.onsuccess = () => resolve(request.result as CachedPublish[]);
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

function deleteCachedEvents(cache: IDBDatabase | null, conversationID: string): Promise<void> {
  if (!cache) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const transaction = cache.transaction('events', 'readwrite');
    const store = transaction.objectStore('events');
    const request = store.index('conversation').openKeyCursor(IDBKeyRange.only(conversationID));
    request.onsuccess = () => {
      const cursor = request.result;
      if (!cursor) return;
      store.delete(cursor.primaryKey);
      cursor.continue();
    };
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(transaction.error);
  });
}

export function useChatClient() {
  const [state, dispatch] = useReducer(reducer, initialState);
  const stateRef = useRef(state);
  const realtimeRef = useRef<RealtimeClient | null>(null);
  const outboxReplayStartedRef = useRef(false);
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
    const activeConversations = result.conversations.filter((conversation) => conversation.status === 'active');
    const activeIDs = new Set(activeConversations.map((conversation) => conversation.id));
    const previous = stateRef.current;
    for (const conversation of previous.conversations) {
      if (!activeIDs.has(conversation.id)) {
        dispatch({ type: 'drop_events', conversation_id: conversation.id });
        await deleteCachedEvents(cacheRef.current, conversation.id);
      }
    }
    if (previous.selected && !activeIDs.has(previous.selected.id)) {
      dispatch({ type: 'selected', selected: null });
      dispatch({ type: 'members', members: [] });
    }
    dispatch({ type: 'conversations', conversations: activeConversations });
    return activeConversations;
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
      const id = crypto.randomUUID();
      const conversation = await retryNetwork(() =>
        api<Conversation>('/api/conversations', {
          method: 'POST',
          body: JSON.stringify({ id, title }),
        }),
      );
      const conversations = await refreshConversations();
      const fresh = conversations.find((item) => item.id === conversation.id) || conversation;
      await selectConversation(fresh);
      return conversation;
    },
    [refreshConversations, selectConversation],
  );

  const sendMessage = useCallback(async (text: string): Promise<boolean> => {
    const current = stateRef.current;
    const conversation = current.selected;
    if (!text || !conversation) return false;
    const clientMessageID = crypto.randomUUID();
    const realtime = realtimeRef.current;
    if (!realtime) throw new Error('实时连接尚未初始化');
    const publish: CachedPublish = {
      id: clientMessageID,
      room_id: conversation.id,
      name: 'message.created',
      profile: 'durable',
      data: { type: 'text', text },
    };
    await saveCachedPublish(cacheRef.current, publish);
    await realtime.publish(publish.room_id, publish.name, publish.profile, publish.data, publish.id);
    await deleteCachedPublish(cacheRef.current, publish.id);
    await refreshConversations();
    return true;
  }, [refreshConversations]);

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
              ? { ...item, last_read_seq: Math.max(item.last_read_seq, readSeq), unread_count: 0 }
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

  const replayOutbox = useCallback(async () => {
    const realtime = realtimeRef.current;
    const cache = cacheRef.current;
    if (!realtime || !cache || outboxReplayStartedRef.current) return;
    outboxReplayStartedRef.current = true;
    try {
      const publishes = await loadCachedPublishes(cache);
      await Promise.all(
        publishes.map(async (publish) => {
          await realtime.publish(publish.room_id, publish.name, publish.profile, publish.data, publish.id);
          await deleteCachedPublish(cache, publish.id);
        }),
      );
    } catch (error) {
      outboxReplayStartedRef.current = false;
      throw error;
    }
  }, []);

  const connect = useCallback(() => {
    if (!realtimeRef.current) {
      realtimeRef.current = new RealtimeClient({
        cursors: () => {
          const cursors: Record<string, number> = {};
          const current = stateRef.current;
          for (const conversation of current.conversations) {
            cursors[conversation.id] = contiguousSeq(conversation, current.events[conversation.id] || {});
          }
          return cursors;
        },
        onConnection: (connected) => dispatch({ type: 'connected', value: connected }),
        onEvent: async (message: DeliveryEvent) => {
          if (message.profile !== 'durable' || !message.sequence) return;
          const item: Event = {
            conversation_id: message.room_id,
            seq: message.sequence,
            id: message.id,
            sender_id: message.actor_id || null,
            client_message_id: message.publish_id || null,
            type: message.name,
            payload: message.data,
            created_at: message.created_at,
          };
          dispatch({ type: 'events', conversation_id: message.room_id, events: [item] });
          await cacheEvents(cacheRef.current, [item]);
          if (message.recovered) return;
          const conversations = await refreshConversations();
          const conversation = conversations.find((entry) => entry.id === message.room_id);
          if (conversation && stateRef.current.selected?.id === conversation.id && message.name !== 'message.created') {
            await loadMembers(conversation);
          }
        },
        onSyncEnd: async () => {
          await refreshConversations();
        },
        onRoomsChanged: async () => {
          await refreshConversations();
        },
        onError: (error) => showNotice(error.message, true),
      });
    }
    realtimeRef.current.connect();
    void replayOutbox().catch((error: Error) => showNotice(error.message, true));
  }, [loadMembers, refreshConversations, replayOutbox, showNotice]);

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

  const logout = useCallback(async () => {
    realtimeRef.current?.close();
    outboxReplayStartedRef.current = false;
    await api('/auth/logout', { method: 'POST', body: '{}' });
    if (cacheRef.current) cacheRef.current.close();
    const user = stateRef.current.user;
    if (user) indexedDB.deleteDatabase(`chat-${user.id}`);
    location.href = '/login';
  }, []);

  useEffect(() => () => realtimeRef.current?.close(), []);

  const bootstrap = useCallback(
    async (user: User) => {
      dispatch({ type: 'user', user });
      cacheRef.current = await openCache(`chat-${user.id}`).catch(() => null);
      const conversations = await refreshConversations();
      await replayOutbox().catch((error: Error) => showNotice(error.message, true));
      return conversations;
    },
    [refreshConversations, replayOutbox, showNotice],
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
    addMember,
    renameConversation,
    removeMember,
    updateMemberRole,
    leaveConversation,
    deleteConversation,
    logout,
  };
}

export type ChatClient = ReturnType<typeof useChatClient>;
