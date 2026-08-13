export type DeliveryProfile = 'durable' | 'ephemeral' | 'stream';

export interface DeliveryEvent {
  op: 'event';
  room_id: string;
  id: string;
  publish_id?: string;
  name: string;
  profile: DeliveryProfile;
  sequence?: number;
  actor_id: string;
  data: Record<string, unknown>;
  created_at: string;
  recovered?: boolean;
  stream?: { id: string; seq: number; final: boolean };
}

interface PublishEnvelope {
  op: 'publish';
  id: string;
  room_id: string;
  name: string;
  profile: DeliveryProfile;
  data: Record<string, unknown>;
}

interface PendingPublish {
  envelope: PublishEnvelope;
  resolve: (ack: DeliveryAck) => void;
  reject: (error: Error) => void;
}

export interface DeliveryAck {
  op: 'ack';
  id: string;
  status: 'accepted' | 'committed';
  event_id?: string;
  sequence?: number;
}

type DeliveryPacket =
  | { op: 'hello'; protocol: string }
  | DeliveryAck
  | DeliveryEvent
  | { op: 'error'; request_id?: string; error: { code: string; message: string; retryable: boolean } }
  | { op: 'room.added' | 'room.removed'; room_id: string }
  | { op: 'sync.begin' | 'sync.end'; room_id: string; sequence?: number };

export interface RealtimeCallbacks {
  cursors: () => Record<string, number>;
  onConnection: (connected: boolean) => void;
  onEvent: (event: DeliveryEvent) => void | Promise<void>;
  onSyncEnd: (roomID: string) => void | Promise<void>;
  onRoomsChanged: () => void | Promise<void>;
  onError: (error: Error) => void;
}

export class RealtimeClient {
  private socket: WebSocket | null = null;
  private reconnectDelay = 500;
  private reconnectTimer: number | null = null;
  private stopped = false;
  private ready = false;
  private readonly pending = new Map<string, PendingPublish>();
  private readonly callbacks: RealtimeCallbacks;

  constructor(callbacks: RealtimeCallbacks) {
    this.callbacks = callbacks;
  }

  connect(): void {
    this.stopped = false;
    if (this.socket && (this.socket.readyState === WebSocket.OPEN || this.socket.readyState === WebSocket.CONNECTING)) return;
    const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const socket = new WebSocket(`${scheme}//${location.host}/realtime`, 'delivery.v1');
    this.socket = socket;
    socket.onopen = () => {
      this.reconnectDelay = 500;
    };
    socket.onmessage = ({ data }) => {
      try {
        this.handlePacket(JSON.parse(data as string) as DeliveryPacket);
      } catch {
        this.callbacks.onError(new Error('realtime service sent an invalid packet'));
        socket.close();
      }
    };
    socket.onerror = () => socket.close();
    socket.onclose = () => {
      if (this.socket === socket) this.socket = null;
      this.ready = false;
      this.callbacks.onConnection(false);
      if (!this.stopped) this.scheduleReconnect();
    };
  }

  close(): void {
    this.stopped = true;
    this.ready = false;
    if (this.reconnectTimer !== null) window.clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
    this.socket?.close();
    this.socket = null;
    const error = new Error('realtime client closed');
    for (const pending of this.pending.values()) pending.reject(error);
    this.pending.clear();
  }

  publish(
    roomID: string,
    name: string,
    profile: DeliveryProfile,
    data: Record<string, unknown>,
    id: string = crypto.randomUUID(),
  ): Promise<DeliveryAck> {
    const existing = this.pending.get(id);
    if (existing) return Promise.reject(new Error(`publish ${id} is already pending`));
    const envelope: PublishEnvelope = { op: 'publish', id, room_id: roomID, name, profile, data };
    const result = new Promise<DeliveryAck>((resolve, reject) => {
      this.pending.set(id, { envelope, resolve, reject });
    });
    this.sendPending(envelope);
    this.connect();
    return result;
  }

  private handlePacket(packet: DeliveryPacket): void {
    switch (packet.op) {
      case 'hello':
        if (packet.protocol !== 'delivery.v1') {
          this.callbacks.onError(new Error(`unsupported realtime protocol ${packet.protocol}`));
          this.socket?.close();
          return;
        }
        this.ready = true;
        this.callbacks.onConnection(true);
        this.send({ op: 'resume', rooms: this.callbacks.cursors() });
        for (const pending of this.pending.values()) this.sendPending(pending.envelope);
        break;
      case 'ack': {
        const pending = this.pending.get(packet.id);
        if (pending) {
          this.pending.delete(packet.id);
          pending.resolve(packet);
        }
        break;
      }
      case 'event':
        void Promise.resolve(this.callbacks.onEvent(packet)).catch(this.callbacks.onError);
        break;
      case 'room.added':
      case 'room.removed':
        void Promise.resolve(this.callbacks.onRoomsChanged()).catch(this.callbacks.onError);
        break;
      case 'error': {
        const error = new Error(packet.error.message);
        if (packet.request_id) {
          const pending = this.pending.get(packet.request_id);
          if (pending && !packet.error.retryable) {
            this.pending.delete(packet.request_id);
            pending.reject(error);
          }
          if (pending && packet.error.retryable) {
            // Retain the stable publish ID and resend it after a fresh hello.
            // The server-side append is idempotent if the response was lost.
            this.socket?.close();
          }
        }
        this.callbacks.onError(error);
        break;
      }
      case 'sync.begin':
        break;
      case 'sync.end':
        void Promise.resolve(this.callbacks.onSyncEnd(packet.room_id)).catch(this.callbacks.onError);
        break;
    }
  }

  private sendPending(envelope: PublishEnvelope): void {
    if (this.ready) this.send(envelope);
  }

  private send(value: unknown): void {
    if (this.socket?.readyState === WebSocket.OPEN) this.socket.send(JSON.stringify(value));
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer !== null) return;
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, this.reconnectDelay);
    this.reconnectDelay = Math.min(this.reconnectDelay * 2, 10_000);
  }
}
