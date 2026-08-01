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

export interface UserLookup {
  user_id: string;
  username?: string;
  display_name?: string;
  email: string;
  email_verified: boolean;
  picture_url?: string;
  avatar_url: string;
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
  payload: Record<string, string | number>;
  created_at: string;
}

export interface Problem {
  error?: { code?: string; message?: string };
}

export interface MessageRequest {
  type: 'message.send';
  request_id: string;
  conversation_id: string;
  client_message_id: string;
  content: { text: string };
}

export interface ReadRequest {
  type: 'read.update';
  request_id: string;
  conversation_id: string;
  seq: number;
}

export interface HelloMessage {
  type: 'hello';
  protocol_version: number;
  user_id: string;
}

export interface EventMessage {
  type: 'conversation.event';
  event: Event;
}

export interface DeletedMessage {
  type: 'conversation.deleted';
  conversation_id: string;
}

export interface StoredMessage {
  type: 'message.stored';
  request_id: string;
  conversation_id: string;
  seq: number;
  message_id: string;
}

export interface ReadUpdatedMessage {
  type: 'read.updated';
  request_id: string;
  conversation_id: string;
  seq: number;
}

export interface ErrorMessage {
  type: 'error';
  request_id?: string;
  code?: string;
  message?: string;
}

export type ServerMessage =
  | HelloMessage
  | EventMessage
  | DeletedMessage
  | StoredMessage
  | ReadUpdatedMessage
  | ErrorMessage;
