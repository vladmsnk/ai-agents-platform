export default function MetricCard({ label, value, accent }: { label: string; value: string | number; accent?: boolean }) {
  return (
    <div className="bg-white rounded-xl border border-gray-200 p-5">
      <p className="text-xs font-medium text-gray-500 mb-1">{label}</p>
      <p className={`text-2xl font-semibold ${accent ? 'text-red-600' : 'text-gray-900'}`}>{value}</p>
    </div>
  )
}
