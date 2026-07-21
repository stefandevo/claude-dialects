import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function pluralize(count: number, singular: string, plural = `${singular}s`) {
  return `${count} ${count === 1 ? singular : plural}`;
}

export function describeVersion(version?: string) {
  return version?.trim() || 'Not installed';
}

export function isValidName(name: string) {
  return name !== '.' && name !== '..' && /^[a-z0-9_][a-z0-9_-]*$/.test(name);
}
