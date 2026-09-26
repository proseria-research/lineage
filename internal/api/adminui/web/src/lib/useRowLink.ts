import { useNavigate } from "react-router-dom";

// A whole list row that opens a page, the way the Models table does. Clicks on anything
// interactive inside the row — a link, a button, a label's explanation, a form control — keep
// their own behaviour.
export function useRowLink() {
  const navigate = useNavigate();
  return (to: string) => ({
    onClick: (e: React.MouseEvent) => {
      if ((e.target as HTMLElement).closest("a, button, input, label, select, textarea, [role=button]")) return;
      navigate(to);
    },
    rowClass: "cursor-pointer transition-colors hover:bg-muted",
  });
}
