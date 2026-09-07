import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getErrorMessage, useGetModelDetailsQuery, useUpsertModelCatalogEntriesMutation } from "@/lib/store";
import { ModelProvider } from "@/lib/types/config";
import { RegisteredUsageModelSchema } from "@/lib/types/schemas";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

// Registered keyless models use the same catalog and provider as the rest of
// the management UI. No API-key records are created for external accounting.
export default function RegisteredModelsTable({ provider }: { provider: ModelProvider }) {
	const canUpdate = useRbac(RbacResource.ModelProvider, RbacOperation.Update);
	const { data, error, isLoading, refetch } = useGetModelDetailsQuery({ provider: provider.name, unfiltered: true, limit: 0 });
	const [update, { isLoading: saving }] = useUpsertModelCatalogEntriesMutation();
	const {
		register,
		handleSubmit,
		reset,
		setError,
		formState: { errors },
	} = useForm<{ model: string }>({ resolver: zodResolver(RegisteredUsageModelSchema), defaultValues: { model: "" } });
	async function add({ model }: { model: string }) {
		try {
			await update([{ provider: provider.name, model, create_if_missing: true }]).unwrap();
			reset();
			toast.success("Model registered");
		} catch (e) {
			setError("model", { message: getErrorMessage(e) });
		}
	}
	return (
		<div className="space-y-4" data-testid="provider-registered-models">
			<div className="flex items-center justify-between">
				<h3 className="font-semibold">Configured models</h3>
				<Button variant="outline" onClick={() => refetch()} data-testid="provider-refresh-models">
					Refresh model list
				</Button>
			</div>
			<p className="text-muted-foreground text-sm">
				STT usage accounting. No API key or inference URL is required. One accounting token equals one millisecond.
			</p>
			{canUpdate && (
				<form onSubmit={handleSubmit(add)} className="space-y-2">
					<div className="flex gap-2">
						<Input {...register("model")} placeholder="Model name" aria-label="Model name" data-testid="provider-model-name" />
						<Button type="submit" disabled={saving} data-testid="provider-model-add">
							Add model
						</Button>
					</div>
					{errors.model && (
						<p className="text-destructive text-sm" role="alert">
							{errors.model.message}
						</p>
					)}
				</form>
			)}
			{!!error && <p role="alert">{getErrorMessage(error)}</p>}
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>Model</TableHead>
						<TableHead>Input USD / ms</TableHead>
						<TableHead>Output USD / ms</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{data?.models.map((model) => (
						<TableRow key={model.name} data-testid={`provider-model-${model.name}`}>
							<TableCell>{model.name}</TableCell>
							<TableCell>{model.input_cost_per_token ?? "Not configured"}</TableCell>
							<TableCell>{model.output_cost_per_token ?? "Not configured"}</TableCell>
						</TableRow>
					))}
					{!isLoading && !data?.models.length && (
						<TableRow>
							<TableCell colSpan={3}>No models registered</TableCell>
						</TableRow>
					)}
				</TableBody>
			</Table>
			<a href="/workspace/custom-pricing/overrides" className="text-sm underline" data-testid="provider-model-pricing">
				Manage pricing overrides
			</a>
		</div>
	);
}