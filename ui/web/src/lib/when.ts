// when formats an event time as text. The design system's Timeline takes
// `when` as a string, so a timeline cannot use the package's <Time> and
// does not follow the Local/UTC toggle: a prop gap to raise upstream
// (INV-0005 finding 8).
export function when(iso: string): string {
  return `${iso.slice(0, 10)} ${iso.slice(11, 16)} UTC`;
}
