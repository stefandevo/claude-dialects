import * as AlertDialog from '@radix-ui/react-alert-dialog';
import { Trash2 } from 'lucide-react';
import { useState } from 'react';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Label } from './ui/label';

interface ConfirmDeleteDialogProps {
  name: string;
  noun: string;
  busy?: boolean;
  onConfirm: () => Promise<void> | void;
}

export function ConfirmDeleteDialog({ name, noun, busy, onConfirm }: ConfirmDeleteDialogProps) {
  const [confirmation, setConfirmation] = useState('');
  const [open, setOpen] = useState(false);

  function handleOpenChange(next: boolean) {
    setOpen(next);
    if (!next) setConfirmation('');
  }

  return (
    <AlertDialog.Root open={open} onOpenChange={handleOpenChange}>
      <AlertDialog.Trigger asChild>
        <Button variant="destructive">
          <Trash2 /> Delete {noun}
        </Button>
      </AlertDialog.Trigger>
      <AlertDialog.Portal>
        <AlertDialog.Overlay className="fixed inset-0 z-50 bg-foreground/45 backdrop-blur-sm" />
        <AlertDialog.Content className="fixed left-1/2 top-1/2 z-50 w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 rounded-lg border bg-background p-6 shadow-2xl">
          <AlertDialog.Title className="text-lg font-semibold">Delete {name}?</AlertDialog.Title>
          <AlertDialog.Description className="mt-2 text-sm leading-relaxed text-muted-foreground">
            This permanently removes the {noun} configuration. Type <strong className="font-mono text-foreground">{name}</strong> exactly to continue.
          </AlertDialog.Description>
          <div className="mt-5 grid gap-2">
            <Label htmlFor={`confirm-${name}`}>Exact {noun} name</Label>
            <Input
              id={`confirm-${name}`}
              autoComplete="off"
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
            />
          </div>
          <div className="mt-6 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <AlertDialog.Cancel asChild><Button variant="outline">Cancel</Button></AlertDialog.Cancel>
            <AlertDialog.Action asChild>
              <Button
                variant="destructive"
                disabled={confirmation !== name || busy}
                onClick={(event) => {
                  event.preventDefault();
                  void Promise.resolve(onConfirm()).then(() => handleOpenChange(false));
                }}
              >
                {busy ? 'Deleting…' : `Delete ${noun}`}
              </Button>
            </AlertDialog.Action>
          </div>
        </AlertDialog.Content>
      </AlertDialog.Portal>
    </AlertDialog.Root>
  );
}
