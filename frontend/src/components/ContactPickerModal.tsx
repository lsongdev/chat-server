import { useEffect, useMemo, useState } from 'react';
import type { Contact } from '../types';

interface ContactPickerModalProps {
  open: boolean;
  title: string;
  confirmLabel: string;
  contacts: Contact[];
  initialSelected?: Contact[];
  children?: React.ReactNode;
  onClose: () => void;
  onConfirm: (selected: Contact[]) => void;
}

export default function ContactPickerModal({
  open,
  title,
  confirmLabel,
  contacts,
  initialSelected = [],
  children,
  onClose,
  onConfirm,
}: ContactPickerModalProps) {
  const [query, setQuery] = useState('');
  const [selectedEmails, setSelectedEmails] = useState<Set<string>>(new Set());

  useEffect(() => {
    if (open) {
      setSelectedEmails(new Set(initialSelected.map((contact) => contact.email)));
      setQuery('');
    }
  }, [open, initialSelected]);

  const filtered = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) return contacts;
    return contacts.filter(
      (contact) =>
        contact.name.toLowerCase().includes(normalized) ||
        contact.email.toLowerCase().includes(normalized),
    );
  }, [contacts, query]);

  const selectedContacts = useMemo(
    () => contacts.filter((contact) => selectedEmails.has(contact.email)),
    [contacts, selectedEmails],
  );

  const toggle = (contact: Contact) => {
    setSelectedEmails((prev) => {
      const next = new Set(prev);
      if (next.has(contact.email)) next.delete(contact.email);
      else next.add(contact.email);
      return next;
    });
  };

  const handleKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Escape') onClose();
  };

  if (!open) return null;

  return (
    <div className="modal-backdrop" onClick={onClose} role="presentation">
      <div className="modal-card" onClick={(event) => event.stopPropagation()} onKeyDown={handleKeyDown}>
        <header className="modal-head">
          <h3>{title}</h3>
          <button type="button" className="secondary" aria-label="关闭" onClick={onClose}>
            ×
          </button>
        </header>
        <div className="modal-body">
          {children}
          <input
            type="search"
            placeholder="搜索姓名或邮箱"
            aria-label="搜索联系人"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          <div className="contact-picker-list">
            {filtered.length === 0 && (
              <p className="muted small" style={{ padding: '12px 0' }}>
                没有匹配的联系人
              </p>
            )}
            {filtered.map((contact) => {
              const isSelected = selectedEmails.has(contact.email);
              return (
                <button
                  key={contact.email}
                  type="button"
                  className={`contact-picker-row${isSelected ? ' selected' : ''}`}
                  aria-pressed={isSelected}
                  onClick={() => toggle(contact)}
                >
                  <img className="avatar small" alt="" loading="lazy" referrerPolicy="no-referrer" src={contact.avatar_url} />
                  <div className="contact-picker-info">
                    <div className="member-email">{contact.name}</div>
                    <div className="member-meta">
                      {contact.note ? `${contact.email} · ${contact.note}` : contact.email}
                    </div>
                  </div>
                  <span className="contact-picker-check" aria-hidden="true">
                    {isSelected ? '✓' : ''}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
        <footer className="modal-foot">
          <button type="button" className="secondary" onClick={onClose}>
            取消
          </button>
          <button type="button" onClick={() => onConfirm(selectedContacts)}>
            {confirmLabel}
          </button>
        </footer>
      </div>
    </div>
  );
}
