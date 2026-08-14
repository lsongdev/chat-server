import { useEffect, useMemo, useRef, useState } from 'react';
import { contiguousSeq, type ChatClient } from '../useChatClient';
import ContactPickerModal from './ContactPickerModal';
import type { Contact, Conversation, Event, Member, User } from '../types';

const SYSTEM_LABELS: Record<string, string> = {
  'conversation.created': '会话已创建',
  'member.joined': '有成员加入了会话',
  'member.left': '有成员退出了会话',
  'member.removed': '有成员被移出会话',
  'conversation.renamed': '会话名称已更新',
};

function titleOf(conversation: Conversation): string {
  return conversation.title || '未命名会话';
}

function userLabel(user: Pick<User, 'id' | 'email' | 'display_name' | 'username'> | Member): string {
  if ('user_id' in user) return user.email || user.display_name || user.username || user.user_id;
  return user.email || user.display_name || user.username || user.id;
}

export default function ChatModule({
  chat,
  conversationRouteActive,
  onOpenConversation,
  onBackToList,
}: {
  chat: ChatClient;
  conversationRouteActive: boolean;
  onOpenConversation: (conversationID: string) => void;
  onBackToList: () => void;
}) {
  const { selected, user, loadContacts, showNotice } = chat;
  const [createTitle, setCreateTitle] = useState('');
  const [renameTitle, setRenameTitle] = useState('');
  const [renameStatus, setRenameStatus] = useState({ message: '', error: false });
  const [pickerOpen, setPickerOpen] = useState(false);
  const [pickerMode, setPickerMode] = useState<'create' | 'add'>('create');
  const [leaveConfirm, setLeaveConfirm] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState(false);
  const [message, setMessage] = useState('');
  const [confirmRemove, setConfirmRemove] = useState<Set<string>>(new Set());
  const composerRef = useRef<HTMLTextAreaElement>(null);
  const messageRef = useRef('');
  const eventsRef = useRef<HTMLElement>(null);

  const events = useMemo(() => {
    if (!selected) return [];
    const bucket = chat.events[selected.id] || {};
    return Object.values(bucket).sort((left, right) => left.seq - right.seq);
  }, [chat.events, selected]);

  useEffect(() => {
    if (selected) setRenameTitle(selected.title || '');
    setLeaveConfirm(false);
    setDeleteConfirm(false);
    setConfirmRemove(new Set());
  }, [selected]);

  useEffect(() => {
    if (!selected) return;
    const readSeq = contiguousSeq(selected, chat.events[selected.id] || {});
    chat.updateRead(selected, readSeq);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected, events]);

  useEffect(() => {
    const container = eventsRef.current;
    if (!container) return;
    const frame = requestAnimationFrame(() => {
      container.scrollTop = container.scrollHeight;
    });
    return () => cancelAnimationFrame(frame);
  }, [selected?.id, events.length]);

  useEffect(() => {
    if (!pickerOpen) return;
    loadContacts().catch((error: Error) => showNotice(error.message, true));
  }, [pickerOpen, loadContacts, showNotice]);

  const openCreatePicker = () => {
    setCreateTitle('');
    setPickerMode('create');
    setPickerOpen(true);
  };

  const openAddPicker = () => {
    setPickerMode('add');
    setPickerOpen(true);
  };

  const handleCreateConfirm = async (contacts: Contact[]) => {
    const title = createTitle.trim() || contacts.map((contact) => contact.name).join('、') || '未命名会话';
    try {
      await chat.createConversation(title, contacts.map((contact) => contact.email));
      setCreateTitle('');
      setPickerOpen(false);
      chat.showNotice('会话已创建。');
    } catch (error) {
      chat.showNotice((error as Error).message, true);
    }
  };

  const handleAddConfirm = async (contacts: Contact[]) => {
    if (!selected || contacts.length === 0) return;
    try {
      await chat.addMembers(selected, contacts.map((contact) => contact.email));
      setPickerOpen(false);
      chat.showNotice(`已添加 ${contacts.length} 位成员。`);
    } catch (error) {
      chat.showNotice((error as Error).message, true);
    }
  };

  const composerSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    const text = messageRef.current;
    if (!text) return;
	try {
	  if (!(await chat.sendMessage(text))) return;
	} catch (error) {
	  chat.showNotice((error as Error).message, true);
	  return;
	}
    messageRef.current = '';
    setMessage('');
    requestAnimationFrame(() => {
      const el = composerRef.current;
      if (el) {
        el.style.height = 'auto';
        el.style.height = `${Math.min(el.scrollHeight, 140)}px`;
      }
    });
  };

  const resizeComposer = () => {
    const el = composerRef.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, 140)}px`;
  };

  const renameSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!selected) return;
    setRenameStatus({ message: '', error: false });
    try {
      await chat.renameConversation(selected, renameTitle.trim());
      setRenameStatus({ message: '会话名称已保存。', error: false });
    } catch (error) {
      setRenameStatus({ message: (error as Error).message, error: true });
    }
  };

  const leaveSubmit = async () => {
    if (!selected) return;
    try {
      await chat.leaveConversation(selected);
      chat.showNotice('已退出会话。');
    } catch (error) {
      chat.showNotice((error as Error).message, true);
    }
  };

  const deleteSubmit = async () => {
    if (!selected) return;
    try {
      await chat.deleteConversation(selected);
      chat.showNotice('会话已删除。');
    } catch (error) {
      chat.showNotice((error as Error).message, true);
    }
  };

  const renderMember = (member: Member) => {
    if (!selected || !user) return null;
    const canManage = selected.status === 'active' && ['owner', 'admin'].includes(selected.role);
    const canRemove =
      selected.status === 'active' &&
      member.status === 'active' &&
      member.user_id !== user.id &&
      member.role !== 'owner' &&
      (selected.role === 'owner' || (selected.role === 'admin' && member.role === 'member'));
    const roleLabel = { owner: '所有者', admin: '管理员', member: '成员' }[member.role] || member.role;
    const statusLabel = member.status === 'active' ? '' : ` · ${member.status}`;
    const meta = `${member.display_name || member.username || '用户'} · ${roleLabel}${statusLabel}`;
    const removing = confirmRemove.has(member.user_id);

    const toggleRole = async () => {
      const next = member.role === 'admin' ? 'member' : 'admin';
      try {
        await chat.updateMemberRole(selected, member.user_id, next);
      } catch (error) {
        chat.showNotice((error as Error).message, true);
      }
    };

    const remove = async () => {
      if (!removing) {
        setConfirmRemove((prev) => new Set(prev).add(member.user_id));
        return;
      }
      try {
        await chat.removeMember(selected, member.user_id);
        chat.showNotice(`已移除 ${member.email || '该成员'}`);
      } catch (error) {
        setConfirmRemove((prev) => {
          const next = new Set(prev);
          next.delete(member.user_id);
          return next;
        });
        chat.showNotice((error as Error).message, true);
      }
    };

    return (
      <div className="member-row" key={member.user_id}>
        <div className="identity-row">
          <img className="avatar" alt="" loading="lazy" referrerPolicy="no-referrer" src={member.avatar_url} />
          <div>
            <div className="member-email">{member.email || '未提供邮件地址'}</div>
            <div className="member-meta">{meta}</div>
          </div>
        </div>
        {canManage && canRemove && selected.role === 'owner' && (
          <button type="button" className="secondary" onClick={toggleRole}>
            {member.role === 'admin' ? '设为成员' : '设为管理员'}
          </button>
        )}
        {canRemove && (
          <button type="button" className="danger" onClick={remove}>
            {removing ? '再次点击确认' : '移除'}
          </button>
        )}
      </div>
    );
  };

  const renderEvent = (item: Event) => {
    if (!user) return null;
    if (item.type === 'message.created') {
      const own = item.sender_id === user.id;
      const sender = chat.members.find((member) => member.user_id === item.sender_id) || (own ? user : null);
      return (
        <div className={`message-row${own ? ' mine' : ''}`} key={item.seq}>
          <img
            className="avatar small"
            alt=""
            loading="lazy"
            referrerPolicy="no-referrer"
            src={sender?.avatar_url || ''}
          />
          <div className="message-body">
            <div className="message-author">{sender ? userLabel(sender) : '未知用户'}</div>
            <div className="message">{String(item.payload.text || '')}</div>
          </div>
        </div>
      );
    }
    return (
      <div className="system" key={item.seq}>
        {SYSTEM_LABELS[item.type] || item.type}
      </div>
    );
  };

  const canManage = selected?.status === 'active' && ['owner', 'admin'].includes(selected.role);
  const composerEnabled = !!selected && selected.status === 'active';
  const availableContacts = useMemo(() => {
    if (pickerMode !== 'add' || !selected) return chat.contacts;
    const existing = new Set(chat.members.map((member) => member.email).filter(Boolean));
    return chat.contacts.filter((contact) => !existing.has(contact.email));
  }, [chat.contacts, chat.members, pickerMode, selected]);
  const empty = events.length === 0 ? (selected ? '还没有消息' : '创建或选择一个会话开始聊天') : null;

  return (
    <section className={`module chat-module ${conversationRouteActive ? 'conversation-open' : 'conversation-list'}`}>
      <aside className="card sidebar">
        <div className="sidebar-head">
          <div className="sidebar-title-row">
            <h2>会话</h2>
            <button type="button" onClick={openCreatePicker}>
              创建会话
            </button>
          </div>
          <p className="muted small">每个会话都可以加入任意成员。</p>
        </div>
        <ul id="conversations" aria-label="会话列表">
          {chat.conversations.length === 0 && (
            <li className="muted small" style={{ padding: '14px 12px' }}>
              还没有会话
            </li>
          )}
          {chat.conversations.map((conversation) => (
            <li key={conversation.id}>
              <button
                type="button"
                className={`conversation-button${chat.selected?.id === conversation.id ? ' active' : ''}`}
                onClick={() => onOpenConversation(conversation.id)}
              >
                <span className="conversation-row">
                  <span>{titleOf(conversation)}</span>
                  {conversation.unread_count > 0 && (
                    <span className="unread" aria-label="有新消息">
                      ●
                    </span>
                  )}
                </span>
              </button>
            </li>
          ))}
        </ul>
      </aside>
      <main className={`card chat${chat.settingsOpen && selected ? ' settings-open' : ''}`}>
        <header className="chat-head">
          <button className="mobile-back secondary" type="button" aria-label="返回会话列表" onClick={onBackToList}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m15 18-6-6 6-6" /></svg>
          </button>
          <div>
            <h2 id="conversation-title">{selected ? titleOf(selected) : '请选择会话'}</h2>
            <p id="connection" className="muted small">
              {chat.connected ? '已连接' : '连接断开，正在重连'}
            </p>
          </div>
          <button
            id="settings"
            className="secondary"
            type="button"
            disabled={!selected}
            aria-expanded={chat.settingsOpen}
            onClick={() => chat.dispatch({ type: 'settings_open', value: !chat.settingsOpen })}
          >
            {chat.settingsOpen ? '收起设置' : '会话设置'}
          </button>
        </header>
        {chat.settingsOpen && selected && (
          <section id="settings-panel" aria-label="会话设置">
            <div className="settings-grid">
              <section className="settings-section">
                <h3>会话名称</h3>
                <form className="inline-form" onSubmit={renameSubmit}>
                  <div className="field">
                    <label htmlFor="rename-title">名称</label>
                    <input id="rename-title" maxLength={100} required value={renameTitle} onChange={(event) => setRenameTitle(event.target.value)} disabled={!canManage} />
                  </div>
                  <button type="submit" disabled={!canManage}>
                    保存
                  </button>
                </form>
                <p id="rename-status" className={`status small${renameStatus.error ? ' error' : ''}`} role="status">
                  {renameStatus.message}
                </p>
              </section>
              <section className="settings-section wide">
                <h3>添加成员</h3>
                <button type="button" disabled={!canManage} onClick={openAddPicker}>
                  选择联系人添加
                </button>
                <p className="muted small" style={{ marginTop: 8 }}>
                  从联系人列表中选择并添加进当前会话。
                </p>
              </section>
              <section className="settings-section wide">
                <h3>会话成员</h3>
                <div id="member-list">
                  {chat.members.length === 0 ? (
                    <p className="muted small">正在加载…</p>
                  ) : (
                    chat.members.map(renderMember)
                  )}
                </div>
              </section>
              <section className="settings-section wide">
                <h3>退出会话</h3>
                <p className="muted small">任何成员都可以退出；所有者退出时会自动转移所有权。</p>
                <button id="leave" className="danger" type="button" onClick={() => setLeaveConfirm(true)} disabled={selected.status !== 'active'}>
                  退出会话
                </button>
                {leaveConfirm && (
                  <div id="leave-confirm" className="danger-zone">
                    <p>确定退出当前会话吗？此操作不会删除其他成员的消息。</p>
                    <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                      <button id="leave-submit" className="danger" type="button" onClick={leaveSubmit}>
                        确认退出
                      </button>
                      <button id="leave-cancel" className="secondary" type="button" onClick={() => setLeaveConfirm(false)}>
                        取消
                      </button>
                    </div>
                  </div>
                )}
                {selected.role === 'owner' && (
                  <div id="delete-conversation-zone" style={{ marginTop: 18 }}>
                    <h3>删除会话</h3>
                    <p className="muted small">仅所有者可用；会为所有成员永久删除会话和消息。</p>
                    <button id="delete-conversation" className="danger" type="button" onClick={() => setDeleteConfirm(true)}>
                      删除会话
                    </button>
                    {deleteConfirm && (
                      <div id="delete-conversation-confirm" className="danger-zone">
                        <p>确定为所有成员永久删除当前会话吗？</p>
                        <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                          <button id="delete-conversation-submit" className="danger" type="button" onClick={deleteSubmit}>
                            确认删除
                          </button>
                          <button id="delete-conversation-cancel" className="secondary" type="button" onClick={() => setDeleteConfirm(false)}>
                            取消
                          </button>
                        </div>
                      </div>
                    )}
                  </div>
                )}
              </section>
            </div>
          </section>
        )}
        <section ref={eventsRef} id="events" aria-live="polite">
          {empty && <p className="empty">{empty}</p>}
          {events.map(renderEvent)}
        </section>
        <form id="composer" onSubmit={composerSubmit}>
          <textarea
            ref={composerRef}
            id="message"
            rows={1}
            maxLength={8192}
            autoComplete="off"
            placeholder="输入消息"
            aria-label="消息"
            disabled={!composerEnabled}
            value={message}
            onChange={(event) => {
              messageRef.current = event.target.value;
              setMessage(event.target.value);
              resizeComposer();
            }}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey && !event.repeat && !event.nativeEvent.isComposing) {
                event.preventDefault();
                event.currentTarget.form?.requestSubmit();
              }
            }}
          />
          <button type="submit" disabled={!composerEnabled}>
            发送
          </button>
        </form>
      </main>
      <ContactPickerModal
        open={pickerOpen}
        title={pickerMode === 'create' ? '创建会话' : '添加成员'}
        confirmLabel={pickerMode === 'create' ? '创建' : '添加'}
        contacts={availableContacts}
        onClose={() => setPickerOpen(false)}
        onConfirm={pickerMode === 'create' ? handleCreateConfirm : handleAddConfirm}
      >
        {pickerMode === 'create' && (
          <div className="field" style={{ marginBottom: 12 }}>
            <label htmlFor="create-title">会话名称</label>
            <input
              id="create-title"
              maxLength={100}
              placeholder="未命名会话"
              value={createTitle}
              onChange={(event) => setCreateTitle(event.target.value)}
            />
          </div>
        )}
      </ContactPickerModal>
    </section>
  );
}
