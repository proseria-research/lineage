import { Routes, Route, Link } from "react-router-dom";
import { Layout } from "@/components/Layout";
import { Logo } from "@/components/Logo";
import Overview from "@/pages/Overview";
import Models from "@/pages/Models";
import ModelDetail from "@/pages/ModelDetail";
import VersionDetail from "@/pages/VersionDetail";
import Compare from "@/pages/Compare";
import Activity from "@/pages/Activity";

function NotFound() {
  return (
    <div className="flex flex-col items-center border border-dashed px-4 py-12 text-center">
      <Logo size={20} className="mb-3" />
      <div className="label-caps mb-2">404 · not found</div>
      <Link to="/" className="text-sm underline underline-offset-4">
        Back to overview
      </Link>
    </div>
  );
}

export default function App() {
  return (
    <Layout>
      <Routes>
        <Route path="/" element={<Overview />} />
        <Route path="/models" element={<Models />} />
        <Route path="/models/:model" element={<ModelDetail />} />
        <Route path="/models/:model/versions/:version" element={<VersionDetail />} />
        <Route path="/models/:model/compare" element={<Compare />} />
        <Route path="/activity" element={<Activity />} />
        <Route path="*" element={<NotFound />} />
      </Routes>
    </Layout>
  );
}
