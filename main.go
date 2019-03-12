package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/gdamore/tcell"
	"github.com/rivo/tview"
	yaml "gopkg.in/yaml.v2"
	k8s "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/clientcmd"
)

type ContextAttribute struct {
	ActiveNamespace string `yaml:"namespace"`
}
type Context struct {
	Name       string           `yaml:"name"`
	Attributes ContextAttribute `yaml:"context"`
}

type config struct {
	ActiveContext string    `yaml:"current-context"`
	Contexts      []Context `yaml:"contexts"`
}

type referenceHelper struct {
	context   string
	namespace string
}

var (
	kubeconfig    config
	expandedNode  *tview.TreeNode
	highlightNode *tview.TreeNode
)

func getNamespacesInContextsCluster(context string) ([]k8s.Namespace, error) {
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{
			ExplicitPath: os.Getenv("KUBECONFIG")},
		&clientcmd.ConfigOverrides{
			CurrentContext: context}).
		ClientConfig()

	if err != nil {
		if reflect.TypeOf(err).String() == "clientcmd.errConfigurationInvalid" {
			return []k8s.Namespace{}, fmt.Errorf("error in config file")
		}

		log.Fatalln(err)
	}

	config.Timeout = 500 * time.Millisecond

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalln(err)
	}

	namespaces, err := clientset.CoreV1().Namespaces().List(v1.ListOptions{})
	if err != nil {
		switch err.(type) {
		case *url.Error:
			return []k8s.Namespace{}, fmt.Errorf("unreachable")
		case *apierrors.StatusError:
			return []k8s.Namespace{}, fmt.Errorf("error from api: " + err.(*apierrors.StatusError).Error())
		default:
			return []k8s.Namespace{}, fmt.Errorf("error")
		}
	}

	return namespaces.Items, nil
}

func switchContext(rh referenceHelper) {
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{
			ExplicitPath: os.Getenv("KUBECONFIG")},
		&clientcmd.ConfigOverrides{}).
		RawConfig()

	if err != nil {
		log.Fatalln(err)
	}

	config.CurrentContext = rh.context
	config.Contexts[rh.context].Namespace = rh.namespace
	configAccess := clientcmd.NewDefaultClientConfigLoadingRules()

	if err := clientcmd.ModifyConfig(configAccess, config, false); err != nil {
		log.Fatalln(err)
	}

	log.Printf("switched to %s/%s", rh.context, rh.namespace)
}

func loadConfig() {
	configContent, err := ioutil.ReadFile(os.Getenv("KUBECONFIG"))
	if err != nil {
		log.Fatalln(err)
	}

	if len(configContent) == 0 {
		log.Fatalln(errors.New("empty configuration file"))
	}

	if err := yaml.Unmarshal(configContent, &kubeconfig); err != nil {
		log.Fatalln(err)
	}
}

func quickSwitch() {
	if len(os.Args) == 1 {
		return
	}

	s := strings.Split(os.Args[1], "/")

	if len(os.Args) == 2 && len(s) == 1 {
		switchContext(referenceHelper{kubeconfig.ActiveContext, os.Args[1]})
		os.Exit(0)
	}

	if len(os.Args) == 2 && len(s) == 2 && contextExists(s[0]) {
		switchContext(referenceHelper{s[0], s[1]})
		os.Exit(0)
	}

	if len(os.Args) == 3 && contextExists(os.Args[1]) {
		switchContext(referenceHelper{os.Args[1], os.Args[2]})
		os.Exit(0)
	}
}

func contextExists(context string) bool {
	for _, ctx := range kubeconfig.Contexts {
		if context == ctx.Name {
			return true
		}
	}

	return false
}

func main() {
	loadConfig()

	if len(os.Args) > 1 {
		quickSwitch()
	}

	app := tview.NewApplication()

	nodeRoot := tview.NewTreeNode("Contexts").
		SetSelectable(false)

	expandedNode = new(tview.TreeNode)
	highlightNode = nodeRoot
	var namespacesInThisContextsCluster []k8s.Namespace
	var getNamespaceError error
	for _, thisContext := range kubeconfig.Contexts {
		nodeContextName := tview.NewTreeNode(" " + thisContext.Name).SetReference(thisContext)

		nodeContextName.Collapse()
		if thisContext.Name == kubeconfig.ActiveContext {
			nodeContextName.SetColor(tcell.ColorGreen).
				SetText(" " + thisContext.Name + " (active)")
		}
		nodeContextName.SetSelectedFunc(func() {
			context := nodeContextName.GetReference().(Context)
			namespacesInThisContextsCluster, getNamespaceError = getNamespacesInContextsCluster(context.Name)
			if getNamespaceError != nil {
				nodeContextName.SetColor(tcell.ColorRed).
					SetText(" " + context.Name + " (" + getNamespaceError.Error() + ")")
				//SetSelectable(false)
			} else if context.Name == kubeconfig.ActiveContext {
				nodeContextName.SetColor(tcell.ColorGreen).
					SetText(" " + context.Name + " (active)")

			} else {
				nodeContextName.SetColor(tcell.ColorTurquoise)
			}
			nodeContextName.SetExpanded(!nodeContextName.IsExpanded())

			if nodeContextName.IsExpanded() && expandedNode != nodeContextName {
				expandedNode.Collapse()
				expandedNode = nodeContextName
			}

			for _, thisNamespace := range namespacesInThisContextsCluster {
				nodeNamespace := tview.NewTreeNode(" " + thisNamespace.Name).
					SetReference(referenceHelper{context.Name, thisNamespace.Name})

				if thisNamespace.Name == context.Attributes.ActiveNamespace {
					nodeNamespace.SetColor(tcell.ColorGreen)
					highlightNode = nodeNamespace
				}

				nodeNamespace.SetSelectedFunc(func() {
					app.Stop()
					switchContext(nodeNamespace.GetReference().(referenceHelper))
				})
				nodeContextName.AddChild(nodeNamespace)
			}
		})

		nodeRoot.AddChild(nodeContextName)

	}

	tree := tview.NewTreeView().
		SetRoot(nodeRoot).
		SetCurrentNode(highlightNode)

	if err := app.SetRoot(tree, true).Run(); err != nil {
		log.Fatalln(err)
	}
}
