import { useState } from "react";
import { NavLink, useNavigate } from "react-router-dom";
import { LayoutGrid, Boxes, Activity, Search } from "lucide-react";
import { cn } from "@/lib/utils";
import { Input } from "@/components/ui/input";
import { Wordmark } from "@/components/Logo";

const nav = [
  { to: "/", label: "Overview", icon: LayoutGrid, end: true },
  { to: "/models", label: "Models", icon: Boxes, end: false },
  { to: "/activity", label: "Activity", icon: Activity, end: false },
];

export function Layout({ children }: { children: React.ReactNode }) {
  const navigate = useNavigate();
  const [q, setQ] = useState("");

  return (
    <div className="grid min-h-screen grid-cols-[13rem_1fr]">
      <aside className="sticky top-0 h-screen border-r bg-card">
        <div className="flex h-12 items-center border-b px-4">
          <Wordmark />
        </div>
        <nav className="p-2">
          {nav.map(({ to, label, icon: Icon, end }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-2.5 border-l-2 border-transparent px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
                  isActive && "border-l-foreground bg-accent font-medium text-foreground",
                )
              }
            >
              <Icon className="h-3.5 w-3.5" strokeWidth={1.5} />
              {label}
            </NavLink>
          ))}
        </nav>
        <div className="absolute bottom-0 w-full border-t px-4 py-2">
          <div className="label-caps">Admin console · :8080</div>
        </div>
      </aside>

      <div className="flex min-w-0 flex-col">
        <header className="sticky top-0 z-10 flex h-12 items-center gap-3 border-b bg-background/95 px-4 backdrop-blur">
          <form
            className="relative w-full max-w-sm"
            onSubmit={(e) => {
              e.preventDefault();
              navigate(q ? `/models?q=${encodeURIComponent(q)}` : "/models");
            }}
          >
            <Search className="pointer-events-none absolute left-2.5 top-2 h-3.5 w-3.5 text-muted-foreground" strokeWidth={1.5} />
            <Input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Search models…"
              className="pl-8"
            />
          </form>
        </header>
        <main className="min-w-0 flex-1 p-6">{children}</main>
      </div>
    </div>
  );
}
