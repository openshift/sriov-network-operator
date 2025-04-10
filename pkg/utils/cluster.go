package utils

import (
	"context"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ObjectHasAnnotationKey checks if a kubernetes object already contains annotation
func ObjectHasAnnotationKey(obj metav1.Object, annoKey string) bool {
	_, hasKey := obj.GetAnnotations()[annoKey]
	return hasKey
}

// ObjectHasAnnotation checks if a kubernetes object already contains annotation
func ObjectHasAnnotation(obj metav1.Object, annoKey string, value string) bool {
	if anno, ok := obj.GetAnnotations()[annoKey]; ok && (anno == value) {
		return true
	}
	return false
}

// AnnotateObject adds annotation to a kubernetes object
func AnnotateObject(ctx context.Context, obj client.Object, key, value string, c client.Client) error {
	original := obj.DeepCopyObject().(client.Object)
	if obj.GetAnnotations() == nil {
		obj.SetAnnotations(map[string]string{})
	}

	if obj.GetAnnotations()[key] != value {
		logger, ok := ctx.Value("logger").(logr.Logger)
		if !ok {
			log.Log.Info("Annotate object",
				"name", obj.GetName(),
				"key", key,
				"value", value)
		} else {
			logger.Info("Annotate object",
				"name", obj.GetName(),
				"key", key,
				"value", value)
		}

		obj.GetAnnotations()[key] = value
		patch := client.MergeFrom(original)
		err := c.Patch(ctx,
			obj, patch)
		if err != nil {
			log.Log.Error(err, "annotateObject(): Failed to patch object")
			return err
		}
	}
	return nil
}

// AnnotateNode add annotation to a node
func AnnotateNode(ctx context.Context, nodeName string, key, value string, c client.Client) error {
	node := &corev1.Node{}
	err := c.Get(ctx, client.ObjectKey{Name: nodeName}, node)
	if err != nil {
		return err
	}

	return AnnotateObject(ctx, node, key, value, c)
}

// labelObject adds label to a kubernetes object
func labelObject(ctx context.Context, obj client.Object, key, value string, c client.Client) error {
	newObj := obj.DeepCopyObject().(client.Object)
	if newObj.GetLabels() == nil {
		newObj.SetLabels(map[string]string{})
	}

	if newObj.GetLabels()[key] != value {
		log.Log.V(2).Info("labelObject(): label object",
			"objectName", obj.GetName(),
			"objectKind", obj.GetObjectKind(),
			"labelKey", key,
			"labelValue", value)
		newObj.GetLabels()[key] = value
		patch := client.MergeFrom(obj)
		err := c.Patch(ctx,
			newObj, patch)
		if err != nil {
			log.Log.Error(err, "labelObject(): Failed to patch object")
			return err
		}
	}

	return nil
}

// removeLabelObject remove a label from a kubernetes object
func removeLabelObject(ctx context.Context, obj client.Object, key string, c client.Client) error {
	newObj := obj.DeepCopyObject().(client.Object)
	if newObj.GetLabels() == nil {
		newObj.SetLabels(map[string]string{})
	}

	_, exist := newObj.GetLabels()[key]
	if exist {
		log.Log.V(2).Info("removeLabelObject(): remove label from object",
			"objectName", obj.GetName(),
			"objectKind", obj.GetObjectKind(),
			"labelKey", key)
		delete(newObj.GetLabels(), key)
		patch := client.MergeFrom(obj)
		err := c.Patch(ctx,
			newObj, patch)
		if err != nil {
			log.Log.Error(err, "removeLabelObject(): Failed to patch object")
			return err
		}
	}

	return nil
}

// LabelNode add label to a node
func LabelNode(ctx context.Context, nodeName string, key, value string, c client.Client) error {
	node := &corev1.Node{}
	err := c.Get(context.TODO(), client.ObjectKey{Name: nodeName}, node)
	if err != nil {
		return err
	}

	return labelObject(ctx, node, key, value, c)
}

func RemoveLabelFromNode(ctx context.Context, nodeName string, key string, c client.Client) error {
	node := &corev1.Node{}
	err := c.Get(context.TODO(), client.ObjectKey{Name: nodeName}, node)
	if err != nil {
		return err
	}

	return removeLabelObject(ctx, node, key, c)
}
