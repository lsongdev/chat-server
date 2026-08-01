import { useMemo, useState } from 'react';
import type { ChatClient } from '../useChatClient';
import type { Contact } from '../types';

export default function ContactsModule({ chat }: { chat: ChatClient }) {
  const [filter, setFilter] = useState('');
  const [editingID, setEditingID] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [note, setNote] = useState('');
  const [status, setStatus] = useState({ message: '', error: false });

  const contacts = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return chat.contacts;
    return chat.contacts.filter(
      (contact) => contact.name.toLowerCase().includes(query) || contact.email.toLowerCase().includes(query),
    );
  }, [chat.contacts, filter]);

  const resetForm = () => {
    setEditingID(null);
    setName('');
    setEmail('');
    setNote('');
    setStatus({ message: '', error: false });
  };

  const startEdit = (contact: Contact) => {
    setEditingID(contact.id);
    setName(contact.name);
    setEmail(contact.email);
    setNote(contact.note || '');
  };

  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    setStatus({ message: '正在保存…', error: false });
    try {
      await chat.saveContact({
        id: editingID || undefined,
        name: name.trim(),
        email: email.trim(),
        note: note.trim(),
      });
      resetForm();
      setStatus({ message: '联系人已保存。', error: false });
    } catch (error) {
      setStatus({ message: (error as Error).message, error: true });
    }
  };

  const remove = async (contact: Contact, button: HTMLButtonElement) => {
    if (button.dataset.confirm !== 'true') {
      button.dataset.confirm = 'true';
      button.textContent = '确认删除';
      return;
    }
    button.disabled = true;
    try {
      await chat.deleteContact(contact.id);
      if (editingID === contact.id) resetForm();
    } catch (error) {
      button.disabled = false;
      setStatus({ message: (error as Error).message, error: true });
    }
  };

  return (
    <section className="module card page">
      <header className="page-head">
        <div>
          <h2>Contacts</h2>
          <p className="muted small">联系人按邮箱同步到你的所有客户端。</p>
        </div>
        <button id="new-contact" type="button" onClick={() => resetForm()}>
          新建联系人
        </button>
      </header>
      <div className="contacts-layout">
        <div>
          <input
            id="contact-filter"
            type="search"
            placeholder="搜索姓名或邮箱"
            aria-label="搜索联系人"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
          />
          <div id="contact-list" className="contact-list" style={{ marginTop: 14 }}>
            {contacts.length === 0 && (
              <p className="muted small" style={{ padding: '18px 0' }}>
                {filter ? '没有匹配的联系人' : '还没有联系人'}
              </p>
            )}
            {contacts.map((contact) => (
              <div className="contact-row" key={contact.id}>
                <div className="identity-row">
                  <img className="avatar" alt="" loading="lazy" referrerPolicy="no-referrer" src={contact.avatar_url} />
                  <div>
                    <div className="member-email">{contact.name}</div>
                    <div className="member-meta">{contact.note ? `${contact.email} · ${contact.note}` : contact.email}</div>
                  </div>
                </div>
                <div className="contact-actions">
                  <button type="button" className="secondary" onClick={() => startEdit(contact)}>
                    编辑
                  </button>
                  <button type="button" className="danger" onClick={(event) => remove(contact, event.currentTarget)}>
                    删除
                  </button>
                </div>
              </div>
            ))}
          </div>
        </div>
        <section className="settings-section">
          <h3 id="contact-form-title">{editingID ? '编辑联系人' : '新建联系人'}</h3>
          <form className="stack-form" onSubmit={save}>
            <div>
              <label htmlFor="contact-name">姓名</label>
              <input id="contact-name" maxLength={80} required value={name} onChange={(event) => setName(event.target.value)} />
            </div>
            <div>
              <label htmlFor="contact-email">邮箱</label>
              <input id="contact-email" type="email" required value={email} onChange={(event) => setEmail(event.target.value)} />
            </div>
            <div>
              <label htmlFor="contact-note">备注</label>
              <textarea id="contact-note" rows={4} maxLength={1000} value={note} onChange={(event) => setNote(event.target.value)} />
            </div>
            <div style={{ display: 'flex', gap: 8 }}>
              <button type="submit">保存</button>
              <button id="contact-cancel" className="secondary" type="button" onClick={resetForm}>
                清空
              </button>
            </div>
          </form>
          <p id="contact-status" className={`status small${status.error ? ' error' : ''}`} role="status">
            {status.message}
          </p>
        </section>
      </div>
    </section>
  );
}
