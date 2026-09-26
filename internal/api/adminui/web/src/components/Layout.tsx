import { useState } from "react";
import { NavLink, useNavigate } from "react-router-dom";
import { Home, Boxes, ShieldCheck, History, Search } from "lucide-react";
import { cn } from "@/lib/utils";
import { Input } from "@/components/ui/input";
import { Wordmark } from "@/components/Logo";

const nav = [
  { to: "/", label: "Home", icon: Home, end: true, hint: "What needs attention" },
  { to: "/models", label: "Models", icon: Boxes, end: false, hint: "Everything registered" },
  { to: "/compliance", label: "Governance", icon: ShieldCheck, end: false, hint: "Risk, reviews and plans" },
  { to: "/activity", label: "Activity", icon: History, end: false, hint: "Who changed what" },
];

export function Layout({ children }: { children: React.ReactNode }) {
  const navigate = useNavigate();
  const [q, setQ] = useState("");

  return (
    <div className="grid min-h-screen grid-cols-1 md:grid-cols-[15rem_1fr]">
      <aside className="border-b bg-card md:sticky md:top-0 md:h-screen md:border-b-0 md:border-r">
        <div className="flex h-14 items-center px-5">
          <Wordmark />
        </div>
        <nav className="flex gap-1 overflow-x-auto px-3 pb-3 md:flex-col md:pb-0">
          {nav.map(({ to, label, icon: Icon, end, hint }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              className={({ isActive }) =>
                cn(
                  "group flex shrink-0 items-center gap-3 rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
                  isActive && "bg-brand-soft text-brand",
                )
              }
            >
              <Icon className="h-4 w-4 shrink-0" strokeWidth={1.75} />
              <span className="flex flex-col leading-tight">
                <span className="font-medium">{label}</span>
                <span className="hidden text-xs font-normal opacity-75 md:block">{hint}</span>
              </span>
            </NavLink>
          ))}
        </nav>
      </aside>

      <div className="flex min-w-0 flex-col">
        <header className="sticky top-0 z-20 flex h-14 items-center gap-3 border-b bg-background/90 px-4 backdrop-blur sm:px-6">
          <form
            className="relative w-full max-w-md"
            onSubmit={(e) => {
              e.preventDefault();
              navigate(q ? `/models?q=${encodeURIComponent(q)}` : "/models");
            }}
          >
            <Search className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" strokeWidth={1.75} />
            <Input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Find a model by name"
              aria-label="Find a model by name"
              className="pl-9"
            />
          </form>
        </header>
        <main className="mx-auto w-full max-w-[84rem] min-w-0 flex-1 px-4 py-6 sm:px-6 sm:py-8">{children}</main>
      </div>
    </div>
  );
}
