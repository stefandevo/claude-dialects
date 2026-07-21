import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ConfirmDeleteDialog } from './ConfirmDeleteDialog';

describe('ConfirmDeleteDialog', () => {
  it('requires an exact, case-sensitive resource name', async () => {
    const onConfirm = vi.fn().mockResolvedValue(undefined);
    render(<ConfirmDeleteDialog name="demo_name" noun="dialect" onConfirm={onConfirm} />);

    fireEvent.click(screen.getByRole('button', { name: /delete dialect/i }));
    const input = screen.getByLabelText(/exact dialect name/i);
    const confirm = screen.getAllByRole('button', { name: /delete dialect/i }).at(-1)!;

    expect(confirm).toBeDisabled();
    fireEvent.change(input, { target: { value: 'Demo_name' } });
    expect(confirm).toBeDisabled();
    fireEvent.change(input, { target: { value: 'demo_name ' } });
    expect(confirm).toBeDisabled();
    fireEvent.change(input, { target: { value: 'demo_name' } });
    expect(confirm).toBeEnabled();

    fireEvent.click(confirm);
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1));
    expect(onConfirm).toHaveBeenCalledWith();
  });
});
