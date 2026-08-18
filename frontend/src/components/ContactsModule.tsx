import { useMemo, useState } from 'react';
import type { ChatClient } from '../useChatClient';
import ContactFormModal from './ContactFormModal';
import type { Contact } from '../types';

export default function ContactsModule({ chat }: { chat: ChatClient }) {
  const [filter, setFilter] = useState('');
  const [formOpen, setFormOpen] = useState(false);
  const [editingContact, setEditingContact] = useState<Contact | null>(null);

  const contacts = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return chat.contacts;
    return chat.contacts.filter(
      (contact) => contact.name.toLowerCase().includes(query) || contact.email.toLowerCase().includes(query),
    );
  }, [chat.contacts, filter]);

  const openNew = () => {
    setEditingContact(null);
    setFormOpen(true);
  };

  const openEdit = (contact: Contact) => {
    setEditingContact(contact);
    setFormOpen(true);
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
    } catch (error) {
      button.disabled = false;
      chat.showNotice((error as Error).message, true);
    }
  };

  return (
    <section className="module card page">
      <header className="page-head">
        <div>
          <h2>Contacts</h2>
          <p className="muted small">联系人按邮箱同步到你的所有客户端。</p>
        </div>
        <button id="new-contact" type="button" onClick={openNew}>
          新建联系人
        </button>
      </header>
      <div className="contacts-layout">
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
                <button type="button" className="secondary" onClick={() => openEdit(contact)}>
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
      <ContactFormModal
        open={formOpen}
        title={editingContact ? '编辑联系人' : '新建联系人'}
        initialName={editingContact?.name}
        initialEmail={editingContact?.email}
        initialNote={editingContact?.note}
        onClose={() => setFormOpen(false)}
        onSave={async (input) => {
          await chat.saveContact({ ...input, id: editingContact?.id });
          setFormOpen(false);
        }}
      />
    </section>
  );
}
