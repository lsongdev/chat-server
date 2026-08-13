export interface User {
  id: string;
  username?: string;
  display_name?: string;
  email?: string;
  email_verified: boolean;
  picture_url?: string;
  avatar_url: string;
  status: string;
}

export interface Conversation {
  id: string;
  title?: string;
  last_seq: number;
  last_read_seq: number;
  unread_count: number;
  joined_seq: number;
  role: string;
  status: string;
  updated_at: string;
}

export interface Member {
  user_id: string;
  username?: string;
  display_name?: string;
  email?: string;
  email_verified: boolean;
  picture_url?: string;
  avatar_url: string;
  role: string;
  status: string;
  joined_seq: number;
}

export interface Contact {
  id: string;
  name: string;
  email: string;
  note?: string;
  linked_user_id?: string | null;
  avatar_url: string;
  created_at: string;
  updated_at: string;
}

export interface Event {
  conversation_id: string;
  seq: number;
  id: string;
  sender_id?: string | null;
  client_message_id?: string | null;
  sender_email?: string;
  sender_name?: string;
  type: string;
  payload: Record<string, unknown>;
  created_at: string;
}

export interface Problem {
  error?: { code?: string; message?: string };
}

export interface HelloMessage {
  type: 'hello';
  protocol_version: number;
  user_id: string;
}

export interface ChangedMessage {
  type: 'conversation.changed';
  conversation_id: string;
  last_seq: number;
}

export interface DeletedMessage {
  type: 'conversation.deleted';
  conversation_id: string;
}

export type ServerMessage =
  | HelloMessage
  | ChangedMessage
  | DeletedMessage;
