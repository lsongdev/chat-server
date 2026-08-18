import { useEffect, useState } from 'react';

interface ContactFormModalProps {
  open: boolean;
  title: string;
  initialName?: string;
  initialEmail?: string;
  initialNote?: string;
  onClose: () => void;
  onSave: (input: { name: string; email: string; note: string }) => void | Promise<void>;
}

export default function ContactFormModal({
  open,
  title,
  initialName = '',
  initialEmail = '',
  initialNote = '',
  onClose,
  onSave,
}: ContactFormModalProps) {
  const [name, setName] = useState(initialName);
  const [email, setEmail] = useState(initialEmail);
  const [note, setNote] = useState(initialNote);
  const [status, setStatus] = useState({ message: '', error: false });
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (open) {
      setName(initialName);
      setEmail(initialEmail);
      setNote(initialNote);
      setStatus({ message: '', error: false });
      setSaving(false);
    }
  }, [open, initialName, initialEmail, initialNote]);

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    setStatus({ message: '正在保存…', error: false });
    setSaving(true);
    try {
      await onSave({ name: name.trim(), email: email.trim(), note: note.trim() });
      onClose();
    } catch (error) {
      setStatus({ message: (error as Error).message, error: true });
    } finally {
      setSaving(false);
    }
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
        <form id="contact-form" className="modal-body" onSubmit={handleSubmit}>
          <div className="field">
            <label htmlFor="contact-form-name">姓名</label>
            <input
              id="contact-form-name"
              maxLength={80}
              required
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <div className="field">
            <label htmlFor="contact-form-email">邮箱</label>
            <input
              id="contact-form-email"
              type="email"
              required
              value={email}
              onChange={(event) => setEmail(event.target.value)}
            />
          </div>
          <div className="field">
            <label htmlFor="contact-form-note">备注</label>
            <textarea
              id="contact-form-note"
              rows={4}
              maxLength={1000}
              value={note}
              onChange={(event) => setNote(event.target.value)}
            />
          </div>
          <p className={`status small${status.error ? ' error' : ''}`} role="status">
            {status.message}
          </p>
        </form>
        <footer className="modal-foot">
          <button type="button" className="secondary" onClick={onClose}>
            取消
          </button>
          <button type="submit" form="contact-form" disabled={saving}>
            保存
          </button>
        </footer>
      </div>
    </div>
  );
}
