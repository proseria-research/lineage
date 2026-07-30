import { Routes, Route, Link } from "react-router-dom";
import { Layout } from "@/components/Layout";
import Overview from "@/pages/Overview";
import Models from "@/pages/Models";
import ModelDetail from "@/pages/ModelDetail";
import VersionDetail from "@/pages/VersionDetail";
import Activity from "@/pages/Activity";

function NotFound() {
  return (
    <div className="border border-dashed px-4 py-12 text-center">
      <div className="label-caps mb-2">404</div>
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
        <Route path="/activity" element={<Activity />} />
        <Route path="*" element={<NotFound />} />
      </Routes>
    </Layout>
  );
}
